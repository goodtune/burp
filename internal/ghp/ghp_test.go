package ghp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goodtune/burp/internal/gh"
)

const reviewPayload = `{"action":"submitted","review":{"user":{"login":"carol"}},` +
	`"pull_request":{"number":7,"user":{"login":"alice"},"head":{"sha":"abc"}},` +
	`"repository":{"name":"api","full_name":"acme/api","owner":{"login":"acme"}}}`

func event(seq int, name, delivery, payload string) string {
	return fmt.Sprintf("id: %d\nevent: %s\ndata: {\"sequence\":%d,\"delivery\":%q,\"event\":%q,\"action\":\"submitted\",\"payload\":%s}\n\n",
		seq, name, seq, delivery, name, payload)
}

type collector struct {
	mu  sync.Mutex
	got []*gh.Delivery
	ch  chan struct{}
}

func newCollector() *collector { return &collector{ch: make(chan struct{}, 16)} }

func (c *collector) handle(d *gh.Delivery) {
	c.mu.Lock()
	c.got = append(c.got, d)
	c.mu.Unlock()
	c.ch <- struct{}{}
}

func (c *collector) wait(t *testing.T, n int) []*gh.Delivery {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for delivery %d of %d", i+1, n)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*gh.Delivery(nil), c.got...)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStreamDeliversAndResumes(t *testing.T) {
	var mu sync.Mutex
	var requests []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Clone(context.Background()))
		n := len(requests)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok-"+fmt.Sprint(n) {
			http.Error(w, `{"message":"Authentication required"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			io.WriteString(w, ": ping\n\n")
			io.WriteString(w, event(41, "pull_request_review", "d-41", reviewPayload))
			io.WriteString(w, event(42, "workflow_job", "d-42", `{}`))
			return // server closes the stream; the client must reconnect
		}
		io.WriteString(w, event(43, "pull_request_review", "d-43", reviewPayload))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	calls := 0
	c := newCollector()
	s := &Subscriber{
		URL:    srv.URL,
		Filter: "github.webhooks.>",
		Token: func(context.Context) (string, error) {
			calls++
			return fmt.Sprintf("tok-%d", calls), nil
		},
		Handle:     c.handle,
		Logger:     quietLogger(),
		MinBackoff: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // before srv.Close, which waits for open streams
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	got := c.wait(t, 3)
	cancel()
	<-done

	if got[0].ID != "d-41" || got[0].Event != "pull_request_review" || got[0].Repo != "acme/api" || len(got[0].Topics) == 0 {
		t.Fatalf("first delivery %+v", got[0])
	}
	if got[1].ID != "d-42" || len(got[1].Topics) != 0 {
		t.Fatalf("second delivery %+v", got[1])
	}
	if got[2].ID != "d-43" {
		t.Fatalf("third delivery %+v", got[2])
	}
	mu.Lock()
	defer mu.Unlock()
	first, second := requests[0], requests[1]
	if first.URL.Path != "/api/webhooks/stream" || first.URL.Query().Get("filter") != "github.webhooks.>" || first.URL.Query().Get("from") != "new" {
		t.Fatalf("first request %s", first.URL)
	}
	if first.Header.Get("Last-Event-ID") != "" || first.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("first request headers %v", first.Header)
	}
	if second.Header.Get("Last-Event-ID") != "42" {
		t.Fatalf("reconnect must resume after the last sequence seen, got %q", second.Header.Get("Last-Event-ID"))
	}
	if calls != 2 {
		t.Fatalf("token must be fetched on every connection, fetched %d times", calls)
	}
}

func TestRejectedTokenRetries(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		if k == 1 {
			http.Error(w, `{"message":"Admin access required"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, event(7, "pull_request_review", "d-7", reviewPayload))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newCollector()
	s := &Subscriber{URL: srv.URL, Token: func(context.Context) (string, error) { return "t", nil },
		Handle: c.handle, Logger: quietLogger(), MinBackoff: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	if got := c.wait(t, 1); got[0].ID != "d-7" {
		t.Fatalf("%+v", got[0])
	}
}

func TestIdleStreamReconnects(t *testing.T) {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		if k == 1 {
			<-r.Context().Done() // silent: no events, no pings
			return
		}
		io.WriteString(w, event(9, "pull_request_review", "d-9", reviewPayload))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := newCollector()
	s := &Subscriber{URL: srv.URL, Token: func(context.Context) (string, error) { return "t", nil },
		Handle: c.handle, Logger: quietLogger(), MinBackoff: 10 * time.Millisecond, IdleTimeout: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	if got := c.wait(t, 1); got[0].ID != "d-9" {
		t.Fatalf("%+v", got[0])
	}
}

func TestCommandToken(t *testing.T) {
	tok := CommandToken("printf ' tok-from-command \\n'")
	got, err := tok(context.Background())
	if err != nil || got != "tok-from-command" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, err = CommandToken("echo nope >&2; exit 3")(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want the command's stderr in the error, got %v", err)
	}
	if _, err := CommandToken("true")(context.Background()); err == nil {
		t.Fatal("want an error for empty output")
	}
}
