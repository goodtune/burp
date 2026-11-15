// Package ghp receives GitHub webhook deliveries from a ghp server's stored
// webhook stream instead of directly from GitHub.
//
// ghp (https://github.com/goodtune/ghp) verifies each delivery's signature,
// stores it in NATS JetStream and serves it to subscribers over server-sent
// events at GET /api/webhooks/stream. The subscriber here keeps one such
// stream open, reconnects with backoff, resumes after the last sequence it
// saw (Last-Event-ID), and hands every delivery to a callback.
//
// It starts from new deliveries rather than replaying the stored log: burp
// fetches page state from GitHub when a page loads, so a delivery only needs
// to arrive while somebody is looking.
package ghp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/goodtune/burp/internal/gh"
)

// Subscriber keeps a ghp webhook stream open.
type Subscriber struct {
	// URL is the ghp server's management URL.
	URL string
	// Filter is a NATS subject filter, e.g. "github.webhooks.>".
	Filter string
	// Token returns the ghp admin session token. It is called on every
	// connection so a renewed token takes effect without a restart.
	Token func(context.Context) (string, error)
	// Handle receives each delivery, in stream order.
	Handle func(*gh.Delivery)
	// HTTPClient must not set a Timeout (the stream is long-lived).
	HTTPClient *http.Client
	Logger     *slog.Logger
	// MinBackoff and MaxBackoff bound the wait between reconnections
	// (defaults 1s and 1m). IdleTimeout ends a stream that has sent
	// nothing, not even ghp's 15-second ping, for that long (default 45s).
	MinBackoff, MaxBackoff, IdleTimeout time.Duration
}

// Run streams until ctx is cancelled.
func (s *Subscriber) Run(ctx context.Context) {
	minB, maxB := s.MinBackoff, s.MaxBackoff
	if minB <= 0 {
		minB = time.Second
	}
	if maxB <= 0 {
		maxB = time.Minute
	}
	backoff := minB
	lastID := ""
	for ctx.Err() == nil {
		start := time.Now()
		n, err := s.stream(ctx, &lastID)
		if ctx.Err() != nil {
			return
		}
		if n > 0 || time.Since(start) > maxB {
			backoff = minB // the connection worked; reconnect promptly
		}
		var se *statusError
		switch {
		case errors.As(err, &se) && (se.code == http.StatusUnauthorized || se.code == http.StatusForbidden):
			s.Logger.Error("ghp: token rejected; check that it belongs to a ghp admin and has not expired", "status", se.code, "message", se.body)
		case err != nil:
			s.Logger.Warn("ghp: webhook stream ended", "error", err, "retry_in", backoff)
		default:
			s.Logger.Info("ghp: webhook stream closed by server", "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxB)
	}
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return fmt.Sprintf("ghp returned %d: %s", e.code, e.body) }

// envelope is the part of ghp's SSE data burp needs.
type envelope struct {
	Delivery string          `json:"delivery"`
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload"`
}

// stream runs one connection and returns how many deliveries it handled.
// lastID is updated as events arrive so the next connection resumes.
func (s *Subscriber) stream(ctx context.Context, lastID *string) (int, error) {
	tok, err := s.Token(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting ghp token: %w", err)
	}
	q := url.Values{"from": {"new"}}
	if s.Filter != "" {
		q.Set("filter", s.Filter)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/api/webhooks/stream?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "text/event-stream")
	if *lastID != "" {
		req.Header.Set("Last-Event-ID", *lastID)
	}
	hc := s.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, &statusError{code: resp.StatusCode, body: strings.TrimSpace(string(b))}
	}
	if *lastID == "" {
		s.Logger.Info("ghp: webhook stream connected", "url", s.URL, "filter", s.Filter)
	} else {
		s.Logger.Info("ghp: webhook stream resumed", "after", *lastID)
	}

	// Watchdog: ghp pings every 15 seconds, so silence means a dead
	// connection that TCP has not noticed yet.
	idle := s.IdleTimeout
	if idle <= 0 {
		idle = 45 * time.Second
	}
	var timedOut atomic.Bool
	watchdog := time.AfterFunc(idle, func() { timedOut.Store(true); cancel() })
	defer watchdog.Stop()

	handled := 0
	var id string
	var data bytes.Buffer
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 32<<20) // payloads up to GitHub's 25 MiB
	for sc.Scan() {
		watchdog.Reset(idle)
		line := sc.Bytes()
		switch {
		case len(line) == 0: // end of event
			if data.Len() > 0 {
				s.dispatch(id, data.Bytes())
				handled++
				if id != "" {
					*lastID = id
				}
			}
			id = ""
			data.Reset()
		case line[0] == ':': // comment / ping
		default:
			field, value, _ := bytes.Cut(line, []byte(":"))
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(field) {
			case "id":
				id = string(value)
			case "data":
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.Write(value)
			}
		}
	}
	if timedOut.Load() {
		return handled, fmt.Errorf("no data from ghp for %s", idle)
	}
	return handled, sc.Err()
}

func (s *Subscriber) dispatch(id string, data []byte) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Event == "" {
		s.Logger.Warn("ghp: skipping unreadable event", "id", id, "error", err)
		return
	}
	s.Handle(gh.ParseDelivery(env.Delivery, env.Event, env.Payload))
}

// CommandToken returns a Token func that runs command with /bin/sh and uses
// its trimmed standard output, for example
// "dotvault kv get --field user_token ghp".
func CommandToken(command string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("token command: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		tok := strings.TrimSpace(stdout.String())
		if tok == "" {
			return "", errors.New("token command printed nothing")
		}
		return tok, nil
	}
}

// StaticToken returns a Token func for a fixed token.
func StaticToken(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}
