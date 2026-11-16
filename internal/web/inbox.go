package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/goodtune/burp/internal/bus"
	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
)

// inboxSignals are the datastar signals of the inbox page.
type inboxSignals struct {
	Filter string `json:"filter"`
}

// inboxSection is one rendered section.
type inboxSection struct {
	Key      string
	Title    string
	Hint     string
	Rows     []inboxRow
	Collapse bool
}

// inboxSectionKeys lists the sections in display order, and
// collapsedByDefault the ones that start closed.
var (
	inboxSectionKeys   = []string{"needs-review", "returned", "approved", "waiting", "drafts", "merged"}
	collapsedByDefault = map[string]bool{"waiting": true, "drafts": true, "merged": true}
)

// Signal names the datastar signal that holds whether the section is open.
// The signals live outside #inbox-sections, so a live re-render keeps the
// sections the viewer opened or closed instead of resetting them. It is
// template.JS because html/template treats data-on:* as an event handler
// and would otherwise quote it; keys are fixed identifiers, never input.
func (s inboxSection) Signal() template.JS {
	return template.JS("open." + strings.ReplaceAll(s.Key, "-", "_"))
}

// inboxOpenSignals is the initial value of the open signals.
func inboxOpenSignals() string {
	parts := make([]string, len(inboxSectionKeys))
	for i, k := range inboxSectionKeys {
		parts[i] = fmt.Sprintf("%s: %t", strings.ReplaceAll(k, "-", "_"), !collapsedByDefault[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// inboxRow is one PR row.
type inboxRow struct {
	gh.PRSummary
	Reason   string
	Reviewed int // files marked reviewed at the current head
}

type inboxView struct {
	Sections []inboxSection
	Warnings []string
	Rendered time.Time
	Filter   string
	Total    int
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	d := s.page(r, "Inbox")
	s.render(w, "inbox", map[string]any{"Page": d, "Filter": r.URL.Query().Get("filter")})
}

// newSSE opens an SSE response.
func newSSE(w http.ResponseWriter, r *http.Request) *datastar.ServerSentEventGenerator {
	return datastar.NewSSE(w, r)
}

// handleInboxStream keeps a stream open: it renders the inbox now, again
// whenever a webhook for the user or one of the shown repositories
// arrives, and every inboxRefresh regardless.
func (s *Server) handleInboxStream(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var sig inboxSignals
	if err := datastar.ReadSignals(r, &sig); err != nil {
		http.Error(w, "bad signals", http.StatusBadRequest)
		return
	}
	sig.Filter = cleanFilter(sig.Filter)
	sse := newSSE(w, r)
	ctx := sse.Context()

	sub := s.bus.Open(bus.UserTopic(u.Login))
	defer sub.Cancel()

	// render shows the inbox from in, or fetches it from GitHub when in is
	// nil (live updates pass live, so a fetch another tab just made is not
	// mistaken for one that saw the event). A failed refresh keeps the inbox
	// on screen with a warning.
	var shown *gh.Inbox
	render := func(in *gh.Inbox, live bool) bool {
		if in == nil {
			client, err := s.clientFor(ctx, u)
			if err == nil {
				in, err = s.fetchInbox(ctx, client, u, sig.Filter, live)
			}
			if err != nil {
				if shown == nil || errors.Is(err, gh.ErrReauthRequired) {
					s.handleAPIError(sse, "inbox-sections", err, "@get('/inbox/stream')")
					return false
				}
				s.logger.Warn("inbox refresh failed", "error", err)
				kept := *shown
				kept.Warnings = append(append([]string(nil), shown.Warnings...), "Could not refresh from GitHub, showing the last result: "+userMessage(err))
				in = &kept
			}
		}
		shown = in
		view, err := s.buildInbox(ctx, in, u, sig.Filter)
		if err != nil {
			s.handleAPIError(sse, "inbox-sections", err, "@get('/inbox/stream')")
			return false
		}
		topics := []string{bus.UserTopic(u.Login)}
		for _, repo := range view.repos {
			parts := strings.SplitN(repo, "/", 2)
			if len(parts) == 2 {
				topics = append(topics, bus.RepoTopic(parts[0], parts[1]))
			}
		}
		sub.SetTopics(topics...)
		// Replace rather than morph: morphing copies each section's default
		// open attribute back onto the live element, closing sections the
		// viewer opened. Fresh elements run data-init, which restores the
		// viewer's choice from the open signals.
		if err := s.patch(sse, "inbox_sections", view, datastar.WithMode(datastar.ElementPatchModeReplace)); err != nil {
			return false
		}
		return true
	}
	// Paint the last result at once; fetch when there is none, or it is old
	// or stale.
	if e, ok := s.cachedInbox(u, sig.Filter); ok && time.Since(e.at) < inboxKeep {
		if !render(e.in, false) && ctx.Err() != nil {
			return
		}
		if e.stale || time.Since(e.at) >= inboxFresh {
			if !render(nil, false) && ctx.Err() != nil {
				return
			}
		}
	} else if !render(nil, false) && ctx.Err() != nil {
		return
	}
	s.streamLoop(ctx, sse, sub, inboxRefresh, s.inboxMinFetch, func() bool { return render(nil, true) })
}

// inboxCacheKey scopes a cached inbox to one user and filter.
func inboxCacheKey(userID int64, filter string) string {
	return fmt.Sprintf("%d\x00%s", userID, filter)
}

func (s *Server) cachedInbox(u *store.User, filter string) (inboxEntry, bool) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	e, ok := s.inboxCache[inboxCacheKey(u.ID, filter)]
	return e, ok
}

// fetchInbox runs the inbox searches on GitHub and caches the result. When
// a page opens (live false), a result another stream fetched moments ago is
// reused; live updates always ask GitHub.
func (s *Server) fetchInbox(ctx context.Context, client *gh.Client, u *store.User, filter string, live bool) (*gh.Inbox, error) {
	if e, ok := s.cachedInbox(u, filter); ok && !live && !e.stale && time.Since(e.at) < inboxDedupe {
		return e.in, nil
	}
	start := time.Now()
	in, err := client.Inbox(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.logger.Debug("inbox fetched", "user", u.Login, "filter", filter, "took", time.Since(start).Round(time.Millisecond))
	s.inboxMu.Lock()
	if len(s.inboxCache) > 256 {
		s.inboxCache = map[string]inboxEntry{}
	}
	s.inboxCache[inboxCacheKey(u.ID, filter)] = inboxEntry{in: in, at: time.Now()}
	s.inboxMu.Unlock()
	return in, nil
}

// staleInbox marks every cached inbox of u stale after u changed something
// through burp, so the next view refreshes instead of trusting the cache.
func (s *Server) staleInbox(u *store.User) {
	prefix := fmt.Sprintf("%d\x00", u.ID)
	s.inboxMu.Lock()
	for k, e := range s.inboxCache {
		if strings.HasPrefix(k, prefix) {
			e.stale = true
			s.inboxCache[k] = e
		}
	}
	s.inboxMu.Unlock()
}

// streamLoop re-renders on bus events (debounced), on the refresh interval
// and keeps the connection alive until the client goes away.
//
// minInterval spaces renders out: an event that arrives sooner than that
// after the last render waits until minInterval has passed, folded together
// with any others that arrive meanwhile.
func (s *Server) streamLoop(ctx context.Context, sse *datastar.ServerSentEventGenerator, sub *bus.Subscription, refresh, minInterval time.Duration, render func() bool) {
	var (
		dirty    bool
		debounce *time.Timer
		dchan    <-chan time.Time
		last     = time.Now()
	)
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	ka := time.NewTicker(keepalive)
	defer ka.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.C:
			if !dirty {
				dirty = true
				wait := eventDebounce
				if left := minInterval - time.Since(last); left > wait {
					wait = left
				}
				debounce = time.NewTimer(wait)
				dchan = debounce.C
			}
		case <-dchan:
			dirty = false
			dchan = nil
			last = time.Now()
			if !render() && ctx.Err() != nil {
				return
			}
			ticker.Reset(refresh)
		case <-ticker.C:
			last = time.Now()
			if !render() && ctx.Err() != nil {
				return
			}
		case <-ka.C:
			// A local (underscore) signal never round-trips to the server;
			// it only keeps proxies from idling the connection.
			if err := sse.PatchSignals([]byte(`{"_keepalive": ` + time.Now().Format("20060102150405") + `}`)); err != nil {
				return
			}
		}
	}
}

type builtInbox struct {
	inboxView
	repos []string
}

func (s *Server) buildInbox(ctx context.Context, in *gh.Inbox, u *store.User, filter string) (*builtInbox, error) {
	rows := func(items []gh.PRSummary, reason func(gh.PRSummary) string, withMarks bool) []inboxRow {
		out := make([]inboxRow, 0, len(items))
		for _, p := range items {
			row := inboxRow{PRSummary: p, Reason: reason(p)}
			if withMarks && p.State == "OPEN" {
				marks, err := s.store.ListFileMarks(ctx, u.ID, store.PRKey{Owner: p.Owner(), Repo: p.Repo(), Number: p.Number})
				if err == nil {
					for _, m := range marks {
						if m.HeadSHA == p.HeadRefOid {
							row.Reviewed++
						}
					}
				}
			}
			out = append(out, row)
		}
		return out
	}
	v := &builtInbox{}
	v.Filter = filter
	v.Rendered = time.Now()
	v.Warnings = in.Warnings
	v.repos = in.Repos()
	v.Sections = []inboxSection{
		{Key: "needs-review", Title: "Needs your review", Hint: "PRs where your review was requested",
			Rows: rows(in.NeedsReview, func(p gh.PRSummary) string {
				if p.ReviewDecision == "CHANGES_REQUESTED" {
					return "re-requested after changes"
				}
				return "review requested"
			}, true)},
		{Key: "returned", Title: "Returned to you", Hint: "your PRs with changes requested",
			Rows: rows(in.Returned, func(p gh.PRSummary) string { return "changes requested" + reviewerNames(p, "CHANGES_REQUESTED") }, false)},
		{Key: "approved", Title: "Approved, ready to merge", Hint: "your PRs that have been approved",
			Rows: rows(in.Approved, func(p gh.PRSummary) string { return "approved" + reviewerNames(p, "APPROVED") }, false)},
		{Key: "waiting", Title: "Waiting on reviewers", Hint: "your open PRs without a decision",
			Rows: rows(in.Waiting, func(p gh.PRSummary) string {
				if rr := p.RequestedReviewers(); len(rr) > 0 {
					return "waiting on " + joinAt(rr)
				}
				return "no reviewer requested"
			}, false)},
		{Key: "drafts", Title: "Drafts", Hint: "your draft PRs",
			Rows: rows(in.Drafts, func(gh.PRSummary) string { return "draft" }, false)},
		{Key: "merged", Title: "Recently merged", Hint: "merged PRs you were involved in",
			Rows: rows(in.Merged, func(gh.PRSummary) string { return "merged" }, false)},
	}
	for i := range v.Sections {
		v.Sections[i].Collapse = collapsedByDefault[v.Sections[i].Key]
	}
	// A pull request belongs to the first section that claims it.
	seen := map[string]bool{}
	for i := range v.Sections {
		kept := v.Sections[i].Rows[:0]
		for _, row := range v.Sections[i].Rows {
			k := row.Repository.NameWithOwner + "#" + strconv.Itoa(row.Number)
			if seen[k] {
				continue
			}
			seen[k] = true
			kept = append(kept, row)
		}
		v.Sections[i].Rows = kept
		v.Total += len(kept)
	}
	return v, nil
}

func reviewerNames(p gh.PRSummary, state string) string {
	var names []string
	for _, r := range p.LatestReviews.Nodes {
		if r.State == state {
			names = append(names, r.Author.Login)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " by " + joinAt(names)
}

func joinAt(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "@" + n
	}
	return strings.Join(out, ", ")
}

// cleanFilter keeps the filter to plain search qualifiers.
func cleanFilter(f string) string {
	f = strings.TrimSpace(f)
	if len(f) > 200 {
		f = f[:200]
	}
	return strings.Join(strings.Fields(f), " ")
}
