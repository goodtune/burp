package web

import (
	"context"
	"net/http"
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

	render := func() bool {
		client, err := s.clientFor(ctx, u)
		if err != nil {
			s.handleAPIError(sse, "inbox-sections", err, "@get('/inbox/stream')")
			return false
		}
		view, err := s.buildInbox(ctx, client, u, sig.Filter)
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
		if err := s.patch(sse, "inbox_sections", view); err != nil {
			return false
		}
		return true
	}
	if !render() && ctx.Err() != nil {
		return
	}
	s.streamLoop(ctx, sse, sub, inboxRefresh, render)
}

// streamLoop re-renders on bus events (debounced), on the refresh interval
// and keeps the connection alive until the client goes away.
func (s *Server) streamLoop(ctx context.Context, sse *datastar.ServerSentEventGenerator, sub *bus.Subscription, refresh time.Duration, render func() bool) {
	var (
		dirty    bool
		debounce *time.Timer
		dchan    <-chan time.Time
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
				debounce = time.NewTimer(eventDebounce)
				dchan = debounce.C
			}
		case <-dchan:
			dirty = false
			dchan = nil
			if !render() && ctx.Err() != nil {
				return
			}
			ticker.Reset(refresh)
		case <-ticker.C:
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

func (s *Server) buildInbox(ctx context.Context, client *gh.Client, u *store.User, filter string) (*builtInbox, error) {
	in, err := client.Inbox(ctx, filter)
	if err != nil {
		return nil, err
	}
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
		{Key: "waiting", Title: "Waiting on reviewers", Hint: "your open PRs without a decision", Collapse: true,
			Rows: rows(in.Waiting, func(p gh.PRSummary) string {
				if rr := p.RequestedReviewers(); len(rr) > 0 {
					return "waiting on " + joinAt(rr)
				}
				return "no reviewer requested"
			}, false)},
		{Key: "drafts", Title: "Drafts", Hint: "your draft PRs", Collapse: true,
			Rows: rows(in.Drafts, func(gh.PRSummary) string { return "draft" }, false)},
		{Key: "merged", Title: "Recently merged", Hint: "merged PRs you were involved in", Collapse: true,
			Rows: rows(in.Merged, func(gh.PRSummary) string { return "merged" }, false)},
	}
	for _, sec := range v.Sections {
		v.Total += len(sec.Rows)
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
