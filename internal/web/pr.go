package web

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/goodtune/burp/internal/bus"
	"github.com/goodtune/burp/internal/diff"
	"github.com/goodtune/burp/internal/gh"
	"github.com/goodtune/burp/internal/store"
)

// maxRenderedLines caps the diff lines rendered for one file.
const maxRenderedLines = 5000

// composer is the single open comment form on the review page.
type composer struct {
	Path      string `json:"path"`
	Side      string `json:"side"`
	Line      int    `json:"line"`
	StartLine int    `json:"startLine"`
	DraftID   string `json:"draftId"`
	Body      string `json:"body"`
}

// prSignals mirrors the datastar signals of the review page. Every action
// posts all of them, so one struct covers every handler.
type prSignals struct {
	File         string   `json:"file"`
	View         string   `json:"view"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	HideReviewed bool     `json:"hideReviewed"`
	Composer     composer `json:"composer"`
	ReplyTo      int64    `json:"replyTo"`
	ReplyBody    string   `json:"replyBody"`
	ReviewOpen   bool     `json:"reviewOpen"`
	ReviewEvent  string   `json:"reviewEvent"`
	ReviewBody   string   `json:"reviewBody"`
	MergeOpen    bool     `json:"mergeOpen"`
	MergeMethod  string   `json:"mergeMethod"`
	HelpOpen     bool     `json:"helpOpen"`
	CommentBody  string   `json:"commentBody"`
	Key          string   `json:"key"`
	MarkPath     string   `json:"markPath"`
	Marked       bool     `json:"marked"`
	ThreadID     string   `json:"threadId"`
	Resolve      bool     `json:"resolve"`
	DraftID      string   `json:"draftId"`
	PRVersion    string   `json:"prVersion,omitempty"`
	Notice       string   `json:"notice"`
}

func defaultSignals() prSignals {
	return prSignals{View: "unified", ReviewEvent: "COMMENT", MergeMethod: "squash"}
}

// revision is a selectable commit in the compare picker.
type revision struct {
	Label    string
	SHA      string
	Short    string
	Headline string
	When     time.Time
	Author   string
	IsHead   bool
	YourLast bool
}

// fileRow is an entry of the file navigator.
type fileRow struct {
	Path       string
	Status     string
	Additions  int
	Deletions  int
	Reviewed   bool
	Stale      bool
	Drafts     int
	Threads    int
	Unresolved int
	Selected   bool
}

// uline is a diff line with everything anchored to it.
type uline struct {
	diff.Line
	Threads  []gh.Thread
	Drafts   []store.Draft
	Composer bool
	Selected bool
}

type urow struct {
	Left  *uline
	Right *uline
}

type hunkView struct {
	Header  string
	Unified []uline
	Split   []urow
}

// fileDetail is the selected file's rendered diff.
type fileDetail struct {
	Path         string
	PreviousPath string
	Status       string
	Additions    int
	Deletions    int
	Truncated    bool
	TooLarge     bool
	Hunks        []hunkView
	Outdated     []gh.Thread
	Reviewed     bool
	Stale        bool
	Index        int
	Count        int
	Prev         string
	Next         string
}

// actionCard is the single most important blocker (Graphite's action card).
type actionCard struct {
	Kind   string // merged, closed, draft, conflict, checks, changes, review, threads, ready, blocked, behind, pending
	Title  string
	Detail string
	URL    string
	CanFix bool
}

type prView struct {
	Page          pageData
	Key           store.PRKey
	PR            *gh.PullRequest
	Sig           prSignals
	SignalsJSON   string
	Head          string
	Base          string
	Revisions     []revision
	Files         []fileRow
	HiddenFiles   int
	Current       *fileDetail
	Drafts        []store.Draft
	Marks         map[string]store.FileMark
	ReviewedCount int
	Checks        []gh.CheckContext
	CheckState    string
	Action        actionCard
	CanReview     bool
	CanMerge      bool
	MergeMethods  []string
	Version       string
	Warnings      []string
	// NewHead shows the "not on the latest revision" banner; Pushed marks
	// the stream's "new commits arrived" variant; Replaced means the
	// revision the user was viewing disappeared (force push).
	NewHead     bool
	Pushed      bool
	Replaced    bool
	Unresolved  int
	ThreadTotal int
}

// URL helpers used by templates.
func (v *prView) Path() string {
	return fmt.Sprintf("/pr/%s/%s/%d", v.Key.Owner, v.Key.Repo, v.Key.Number)
}

// ---- handlers -----------------------------------------------------------

func (s *Server) handlePR(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	key, err := prKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sig := defaultSignals()
	q := r.URL.Query()
	sig.File = q.Get("file")
	if v := q.Get("view"); v == "split" {
		sig.View = "split"
	}
	sig.Base, sig.Head = q.Get("base"), q.Get("head")
	client, err := s.clientFor(r.Context(), u)
	if err != nil {
		if errors.Is(err, gh.ErrReauthRequired) {
			http.Redirect(w, r, "/?reason=expired&return_to="+r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		s.renderError(w, r, http.StatusBadGateway, userMessage(err))
		return
	}
	view, err := s.buildPR(r.Context(), client, u, key, sig)
	if err != nil {
		var ae *gh.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			s.renderError(w, r, http.StatusNotFound, "Pull request not found, or you do not have access to it.")
			return
		}
		if errors.Is(err, gh.ErrReauthRequired) {
			http.Redirect(w, r, "/?reason=expired", http.StatusSeeOther)
			return
		}
		s.renderError(w, r, http.StatusBadGateway, userMessage(err))
		return
	}
	view.Page = s.page(r, fmt.Sprintf("%s/%s #%d", key.Owner, key.Repo, key.Number))
	s.render(w, "pr", view)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.WriteHeader(status)
	s.render(w, "error", map[string]any{"Page": s.page(r, "Error"), "Message": msg, "Status": status})
}

// prAction is the common shape of every review page action: read signals,
// mutate, re-render the file navigator and diff pane.
func (s *Server) prAction(w http.ResponseWriter, r *http.Request, act func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error) {
	u := userFrom(r.Context())
	key, err := prKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	sig := defaultSignals()
	if err := datastar.ReadSignals(r, &sig); err != nil {
		http.Error(w, "bad signals", http.StatusBadRequest)
		return
	}
	sse := newSSE(w, r)
	ctx := sse.Context()
	client, err := s.clientFor(ctx, u)
	if err != nil {
		s.handleAPIError(sse, "pr-notice", err, "")
		return
	}
	var actErr error
	if act != nil {
		if actErr = act(ctx, client, u, key, &sig); actErr != nil {
			s.handleAPIError(sse, "pr-notice", actErr, "")
			// Fall through: still re-render so the page reflects reality.
		}
	}
	view, err := s.buildPR(ctx, client, u, key, sig)
	if err != nil {
		s.handleAPIError(sse, "pr-notice", err, "@get('"+prPath(key)+"/view')")
		return
	}
	view.Page.User = u
	_ = s.patch(sse, "pr_files", view)
	_ = s.patch(sse, "pr_diff", view)
	_ = s.patch(sse, "pr_toolbar", view)
	_ = s.patch(sse, "pr_header", view)
	if actErr == nil {
		_ = s.patch(sse, "pr_notice_empty", nil)
	}
	// prVersion is only ever patched by the stream; patching it here would
	// re-trigger the page's refresh reaction.
	sig = view.Sig
	sig.PRVersion = ""
	_ = sse.MarshalAndPatchSignals(sig)
}

func prPath(key store.PRKey) string {
	return fmt.Sprintf("/pr/%s/%s/%d", key.Owner, key.Repo, key.Number)
}

func (s *Server) handlePRView(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, nil)
}

func (s *Server) handlePRKey(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		k := sig.Key
		sig.Key = ""
		switch k {
		case "Escape":
			sig.Composer = composer{}
			sig.ReplyTo = 0
			sig.ReviewOpen, sig.MergeOpen, sig.HelpOpen = false, false, false
		case "?":
			sig.HelpOpen = !sig.HelpOpen
		case "v":
			if sig.View == "split" {
				sig.View = "unified"
			} else {
				sig.View = "split"
			}
		case "a":
			sig.ReviewOpen, sig.ReviewEvent = true, "APPROVE"
		case "c":
			sig.ReviewOpen, sig.ReviewEvent = true, "COMMENT"
		case "j", "k", "n", "r":
			// Needs the file list; handled after build via key carried over.
			sig.Key = k
		}
		return nil
	})
}

func (s *Server) handlePRMark(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		path := sig.MarkPath
		sig.MarkPath = ""
		if path == "" {
			return nil
		}
		if !sig.Marked {
			return s.store.ClearFileMark(ctx, u.ID, key, path)
		}
		head := sig.Head
		if head == "" {
			pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
			if err != nil {
				return err
			}
			head = pr.HeadRefOid
		}
		return s.store.SetFileMark(ctx, &store.FileMark{UserID: u.ID, Owner: key.Owner, Repo: key.Repo, Number: key.Number, Path: path, HeadSHA: head})
	})
}

func (s *Server) handlePRDraft(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		c := sig.Composer
		body := strings.TrimSpace(c.Body)
		if c.Path == "" || c.Line <= 0 || body == "" {
			return errors.New("a comment needs a line and some text")
		}
		if c.Side != "LEFT" {
			c.Side = "RIGHT"
		}
		if len(body) > 65536 {
			return errors.New("comment is too long")
		}
		head := sig.Head
		if head == "" {
			pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
			if err != nil {
				return err
			}
			head = pr.HeadRefOid
		}
		d := &store.Draft{ID: c.DraftID, UserID: u.ID, Owner: key.Owner, Repo: key.Repo, Number: key.Number,
			Path: c.Path, Side: c.Side, Line: c.Line, StartLine: c.StartLine, CommitSHA: head, Body: body}
		if d.ID == "" {
			d.ID = randomToken()[:16]
		} else {
			existing, err := s.store.GetDraft(ctx, u.ID, d.ID)
			if err != nil {
				return err
			}
			d.CreatedAt = existing.CreatedAt
		}
		if err := s.store.PutDraft(ctx, d); err != nil {
			return err
		}
		sig.Composer = composer{}
		return nil
	})
}

func (s *Server) handlePRDraftDelete(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		id := sig.DraftID
		sig.DraftID = ""
		sig.Composer = composer{}
		if id == "" {
			return nil
		}
		return s.store.DeleteDraft(ctx, u.ID, id)
	})
}

func (s *Server) handlePRSubmit(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		event := sig.ReviewEvent
		switch event {
		case "APPROVE", "REQUEST_CHANGES", "COMMENT":
		default:
			return errors.New("choose approve, request changes or comment")
		}
		drafts, err := s.store.ListDrafts(ctx, u.ID, key)
		if err != nil {
			return err
		}
		body := strings.TrimSpace(sig.ReviewBody)
		if event == "COMMENT" && body == "" && len(drafts) == 0 {
			return errors.New("a comment review needs a summary or at least one draft comment")
		}
		pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
		if err != nil {
			return err
		}
		comments := make([]gh.ReviewComment, 0, len(drafts))
		for _, d := range drafts {
			comments = append(comments, gh.ReviewComment{Path: d.Path, Body: d.Body, Line: d.Line, Side: d.Side, StartLine: d.StartLine})
		}
		if err := client.SubmitReview(ctx, key.Owner, key.Repo, key.Number, pr.HeadRefOid, event, body, comments); err != nil {
			return fmt.Errorf("submitting review: %w", err)
		}
		if err := s.store.DeleteDrafts(ctx, u.ID, key); err != nil {
			s.logger.Error("clearing drafts after submit", "error", err)
		}
		sig.ReviewOpen, sig.ReviewBody, sig.ReviewEvent = false, "", "COMMENT"
		sig.Notice = fmt.Sprintf("Review submitted (%s) with %d comment(s).", strings.ToLower(strings.ReplaceAll(event, "_", " ")), len(comments))
		s.bus.Publish(bus.Event{Topic: bus.PRTopic(key.Owner, key.Repo, key.Number), Kind: "local", Action: "review"})
		return nil
	})
}

func (s *Server) handlePRReply(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		body := strings.TrimSpace(sig.ReplyBody)
		if sig.ReplyTo == 0 || body == "" {
			return errors.New("reply needs some text")
		}
		if err := client.Reply(ctx, key.Owner, key.Repo, key.Number, sig.ReplyTo, body); err != nil {
			return err
		}
		sig.ReplyTo, sig.ReplyBody = 0, ""
		return nil
	})
}

func (s *Server) handlePRResolve(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		id := sig.ThreadID
		sig.ThreadID = ""
		if id == "" {
			return errors.New("missing thread")
		}
		return client.ResolveThread(ctx, id, sig.Resolve)
	})
}

func (s *Server) handlePRComment(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		body := strings.TrimSpace(sig.CommentBody)
		if body == "" {
			return errors.New("comment needs some text")
		}
		if err := client.IssueComment(ctx, key.Owner, key.Repo, key.Number, body); err != nil {
			return err
		}
		sig.CommentBody = ""
		sig.Notice = "Comment posted."
		return nil
	})
}

func (s *Server) handlePRMerge(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		method := sig.MergeMethod
		switch method {
		case "merge", "squash", "rebase":
		default:
			return errors.New("choose a merge method")
		}
		pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
		if err != nil {
			return err
		}
		if err := client.Merge(ctx, key.Owner, key.Repo, key.Number, method, pr.HeadRefOid); err != nil {
			return fmt.Errorf("merging: %w", err)
		}
		sig.MergeOpen = false
		sig.Notice = "Merged."
		s.bus.Publish(bus.Event{Topic: bus.PRTopic(key.Owner, key.Repo, key.Number), Kind: "local", Action: "merge"})
		return nil
	})
}

func (s *Server) handlePRReady(w http.ResponseWriter, r *http.Request) {
	s.prAction(w, r, func(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig *prSignals) error {
		pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
		if err != nil {
			return err
		}
		if err := client.Ready(ctx, pr.ID); err != nil {
			return err
		}
		sig.Notice = "Marked ready for review."
		return nil
	})
}

// handlePRStream pushes header updates when GitHub reports a change and
// bumps the prVersion signal so the page can refresh its diff pane.
func (s *Server) handlePRStream(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	key, err := prKey(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sig := defaultSignals()
	if err := datastar.ReadSignals(r, &sig); err != nil {
		http.Error(w, "bad signals", http.StatusBadRequest)
		return
	}
	sse := newSSE(w, r)
	ctx := sse.Context()
	last := sig.PRVersion
	lastHead := sig.Head
	sub := s.bus.Open(bus.PRTopic(key.Owner, key.Repo, key.Number))
	defer sub.Cancel()

	render := func() bool {
		client, err := s.clientFor(ctx, u)
		if err != nil {
			s.handleAPIError(sse, "pr-notice", err, "")
			return false
		}
		pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
		if err != nil {
			s.handleAPIError(sse, "pr-notice", err, "")
			return false
		}
		sub.SetTopics(bus.PRTopic(key.Owner, key.Repo, key.Number), bus.SHATopic(pr.HeadRefOid))
		v := prVersion(pr)
		if v == last {
			return true
		}
		last = v
		view := &prView{Key: key, PR: pr, Sig: sig, Version: v}
		view.Sig.PRVersion = v
		s.decorateHeader(view)
		pushed := lastHead != "" && lastHead != pr.HeadRefOid
		lastHead = pr.HeadRefOid
		if pushed {
			// New commits: show the banner and let the user decide when to
			// switch revisions instead of changing the diff under them.
			view.NewHead, view.Pushed = true, true
			return s.patch(sse, "pr_header", view) == nil
		}
		if err := s.patch(sse, "pr_header", view); err != nil {
			return false
		}
		return sse.PatchSignals([]byte(`{"prVersion": "`+v+`"}`)) == nil
	}
	// The page rendered the current state; only push on change.
	s.streamLoop(ctx, sse, sub, 5*time.Minute, render)
}

// prVersion fingerprints the parts of a PR the header shows.
func prVersion(pr *gh.PullRequest) string {
	h := sha256.New()
	h.Write([]byte(pr.HeadRefOid + pr.State + pr.ReviewDecision + pr.Mergeable + pr.MergeStateStatus))
	h.Write([]byte(pr.UpdatedAt.Format(time.RFC3339Nano)))
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(len(pr.ReviewThreads.Nodes)))
	h.Write(b[:])
	for _, t := range pr.ReviewThreads.Nodes {
		h.Write([]byte(t.ID))
		if t.IsResolved {
			h.Write([]byte{1})
		}
		binary.LittleEndian.PutUint64(b[:], uint64(len(t.Comments.Nodes)))
		h.Write(b[:])
	}
	state, checks := pr.Checks()
	h.Write([]byte(state))
	for _, c := range checks {
		h.Write([]byte(c.Label() + c.Outcome()))
	}
	for _, rv := range pr.Reviews.Nodes {
		h.Write([]byte(rv.ID + rv.State))
	}
	binary.LittleEndian.PutUint64(b[:], uint64(len(pr.Comments.Nodes)))
	h.Write(b[:])
	sum := h.Sum(nil)
	return fmt.Sprintf("%x", sum[:8])
}

// ---- view building ------------------------------------------------------

func (s *Server) buildPR(ctx context.Context, client *gh.Client, u *store.User, key store.PRKey, sig prSignals) (*prView, error) {
	pr, err := client.PullRequest(ctx, key.Owner, key.Repo, key.Number)
	if err != nil {
		return nil, err
	}
	view := &prView{Key: key, PR: pr, Sig: sig}
	view.Version = prVersion(pr)
	view.Sig.PRVersion = view.Version
	if view.Sig.View != "split" {
		view.Sig.View = "unified"
	}
	s.decorateHeader(view)

	// Resolve the compare range.
	view.Head = pr.HeadRefOid
	if sig.Head != "" && sig.Head != pr.HeadRefOid {
		if commitKnown(pr, sig.Head) {
			view.Head = sig.Head
			view.NewHead = true
		} else {
			view.Replaced = true
		}
	}
	view.Sig.Head = view.Head
	if sig.Base != "" && sig.Base != view.Head {
		view.Base = sig.Base
	}
	view.Sig.Base = view.Base

	marks, err := s.store.ListFileMarks(ctx, u.ID, key)
	if err != nil {
		return nil, err
	}
	view.Marks = make(map[string]store.FileMark, len(marks))
	for _, m := range marks {
		view.Marks[m.Path] = m
	}
	drafts, err := s.store.ListDrafts(ctx, u.ID, key)
	if err != nil {
		return nil, err
	}
	view.Drafts = drafts

	view.Revisions = buildRevisions(pr, marks)
	for i := range view.Revisions {
		if view.Revisions[i].SHA == view.Base && view.Base != "" {
			view.Revisions[i].Label += " (base)"
		}
	}

	// Files for the range.
	files, err := s.rangeFiles(ctx, client, key, pr, view.Base, view.Head)
	if err != nil {
		return nil, err
	}

	// Which marked files changed since the mark.
	stale := map[string]bool{}
	for _, m := range marks {
		if m.HeadSHA == pr.HeadRefOid {
			continue
		}
		changed, err := s.changedBetween(ctx, client, key, m.HeadSHA, pr.HeadRefOid)
		if err != nil {
			view.Warnings = append(view.Warnings, "could not compare with your earlier review: "+userMessage(err))
			continue
		}
		if changed[m.Path] {
			stale[m.Path] = true
		}
	}

	threadsByPath := map[string][]gh.Thread{}
	for _, t := range pr.ReviewThreads.Nodes {
		threadsByPath[t.Path] = append(threadsByPath[t.Path], t)
	}
	draftsByPath := map[string][]store.Draft{}
	for _, d := range drafts {
		draftsByPath[d.Path] = append(draftsByPath[d.Path], d)
	}

	// File navigator.
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	all := make([]fileRow, 0, len(files))
	for _, f := range files {
		row := fileRow{Path: f.Path, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Drafts: len(draftsByPath[f.Path])}
		if m, ok := view.Marks[f.Path]; ok {
			row.Reviewed = m.HeadSHA == pr.HeadRefOid || !stale[f.Path]
			row.Stale = stale[f.Path]
			if row.Stale {
				row.Reviewed = false
			}
		}
		for _, t := range threadsByPath[f.Path] {
			row.Threads++
			if !t.IsResolved {
				row.Unresolved++
			}
		}
		if row.Reviewed {
			view.ReviewedCount++
		}
		all = append(all, row)
	}

	// Keyboard navigation that depends on the file list.
	if len(all) > 0 {
		idx := indexOf(all, view.Sig.File)
		switch view.Sig.Key {
		case "j":
			idx = min(idx+1, len(all)-1)
		case "k":
			idx = max(idx-1, 0)
		case "n":
			start := idx
			for i := 1; i <= len(all); i++ {
				c := (start + i) % len(all)
				if !all[c].Reviewed {
					idx = c
					break
				}
			}
		case "r":
			if idx >= 0 {
				row := &all[idx]
				if row.Reviewed {
					_ = s.store.ClearFileMark(ctx, u.ID, key, row.Path)
					row.Reviewed = false
					view.ReviewedCount--
				} else {
					_ = s.store.SetFileMark(ctx, &store.FileMark{UserID: u.ID, Owner: key.Owner, Repo: key.Repo, Number: key.Number, Path: row.Path, HeadSHA: pr.HeadRefOid})
					row.Reviewed, row.Stale = true, false
					view.ReviewedCount++
				}
			}
		}
		view.Sig.Key = ""
		if idx < 0 {
			idx = 0
		}
		view.Sig.File = all[idx].Path
		all[idx].Selected = true
	} else {
		view.Sig.File = ""
		view.Sig.Key = ""
	}

	if view.Sig.HideReviewed {
		for _, row := range all {
			if row.Reviewed && !row.Selected {
				view.HiddenFiles++
				continue
			}
			view.Files = append(view.Files, row)
		}
	} else {
		view.Files = all
	}

	// Selected file diff.
	if view.Sig.File != "" {
		for i, f := range files {
			if f.Path != view.Sig.File {
				continue
			}
			det := buildFileDetail(f, threadsByPath[f.Path], draftsByPath[f.Path], view.Sig.Composer)
			det.Index, det.Count = i+1, len(files)
			if i > 0 {
				det.Prev = files[i-1].Path
			}
			if i+1 < len(files) {
				det.Next = files[i+1].Path
			}
			if row := all[indexOf(all, f.Path)]; true {
				det.Reviewed, det.Stale = row.Reviewed, row.Stale
			}
			view.Current = det
			break
		}
	}
	if view.Sig.Composer.Path != "" && (view.Current == nil || view.Current.Path != view.Sig.Composer.Path) {
		// Composer belongs to another file: keep it out of the way.
		view.Sig.Composer = composer{}
	}

	js, _ := json.Marshal(view.Sig)
	view.SignalsJSON = string(js)
	return view, nil
}

func commitKnown(pr *gh.PullRequest, sha string) bool {
	for _, c := range pr.Commits.Nodes {
		if c.Commit.Oid == sha {
			return true
		}
	}
	return false
}

func indexOf(rows []fileRow, path string) int {
	for i, r := range rows {
		if r.Path == path {
			return i
		}
	}
	return -1
}

// decorateHeader fills the parts of the view the header template needs.
func (s *Server) decorateHeader(v *prView) {
	pr := v.PR
	v.CheckState, v.Checks = pr.Checks()
	v.Unresolved, v.ThreadTotal = pr.UnresolvedThreads()
	v.CanReview = !pr.ViewerDidAuthor && pr.State == "OPEN"
	if pr.Repository.SquashMergeAllowed {
		v.MergeMethods = append(v.MergeMethods, "squash")
	}
	if pr.Repository.MergeCommitAllowed {
		v.MergeMethods = append(v.MergeMethods, "merge")
	}
	if pr.Repository.RebaseMergeAllowed {
		v.MergeMethods = append(v.MergeMethods, "rebase")
	}
	if len(v.MergeMethods) > 0 && !containsStr(v.MergeMethods, v.Sig.MergeMethod) {
		v.Sig.MergeMethod = v.MergeMethods[0]
	}
	perm := pr.Repository.ViewerPermission
	canPush := perm == "WRITE" || perm == "MAINTAIN" || perm == "ADMIN"
	v.CanMerge = pr.State == "OPEN" && !pr.IsDraft && canPush && pr.Mergeable != "CONFLICTING" && len(v.MergeMethods) > 0
	v.Action = computeAction(pr, v.CheckState, v.Checks, v.Unresolved, canPush)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func computeAction(pr *gh.PullRequest, checkState string, checks []gh.CheckContext, unresolved int, canPush bool) actionCard {
	switch {
	case pr.Merged:
		return actionCard{Kind: "merged", Title: "Merged", Detail: "This pull request has been merged."}
	case pr.State == "CLOSED":
		return actionCard{Kind: "closed", Title: "Closed", Detail: "This pull request was closed without merging."}
	case pr.IsDraft:
		return actionCard{Kind: "draft", Title: "Draft", Detail: "Mark it ready for review when it is.", CanFix: pr.ViewerDidAuthor}
	case pr.Mergeable == "CONFLICTING":
		return actionCard{Kind: "conflict", Title: "Merge conflicts", Detail: "Resolve conflicts with " + pr.BaseRefName + " on the branch."}
	}
	if checkState == "FAILURE" || checkState == "ERROR" {
		var failing []string
		for _, c := range checks {
			if c.Outcome() == "failure" {
				failing = append(failing, c.Label())
			}
		}
		url := ""
		for _, c := range checks {
			if c.Outcome() == "failure" && c.URL() != "" {
				url = c.URL()
				break
			}
		}
		return actionCard{Kind: "checks", Title: "Checks failing", Detail: strings.Join(failing, ", "), URL: url}
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		var who []string
		for _, r := range pr.LatestReviews.Nodes {
			if r.State == "CHANGES_REQUESTED" {
				who = append(who, "@"+r.Author.Login)
			}
		}
		return actionCard{Kind: "changes", Title: "Changes requested", Detail: "by " + strings.Join(who, ", ")}
	}
	if checkState == "PENDING" || checkState == "EXPECTED" {
		return actionCard{Kind: "pending", Title: "Checks running", Detail: "Waiting for CI to finish."}
	}
	if pr.ReviewDecision == "REVIEW_REQUIRED" {
		return actionCard{Kind: "review", Title: "Needs approval", Detail: "An approving review is required before merging.", CanFix: !pr.ViewerDidAuthor}
	}
	if unresolved > 0 {
		return actionCard{Kind: "threads", Title: fmt.Sprintf("%d unresolved thread(s)", unresolved), Detail: "Resolve the open conversations."}
	}
	switch pr.MergeStateStatus {
	case "BEHIND":
		return actionCard{Kind: "behind", Title: "Behind " + pr.BaseRefName, Detail: "Update the branch before merging."}
	case "BLOCKED":
		return actionCard{Kind: "blocked", Title: "Blocked", Detail: "Branch protection rules are not satisfied yet."}
	case "DIRTY":
		return actionCard{Kind: "conflict", Title: "Merge conflicts", Detail: "Resolve conflicts with " + pr.BaseRefName + " on the branch."}
	}
	if canPush {
		return actionCard{Kind: "ready", Title: "Ready to merge", Detail: "Approved, checks green, no conflicts.", CanFix: true}
	}
	return actionCard{Kind: "ready", Title: "Ready to merge", Detail: "Someone with push access can merge this."}
}

// buildRevisions lists the PR commits as r1..rN plus the commits of the
// user's marks that are no longer in the branch (after a force push).
func buildRevisions(pr *gh.PullRequest, marks []store.FileMark) []revision {
	out := make([]revision, 0, len(pr.Commits.Nodes)+1)
	seen := map[string]bool{}
	for i, n := range pr.Commits.Nodes {
		c := n.Commit
		r := revision{Label: fmt.Sprintf("r%d", i+1), SHA: c.Oid, Short: c.AbbreviatedOid, Headline: c.MessageHeadline, When: c.CommittedDate}
		if c.Author.User != nil {
			r.Author = c.Author.User.Login
		} else {
			r.Author = c.Author.Name
		}
		r.IsHead = c.Oid == pr.HeadRefOid
		out = append(out, r)
		seen[c.Oid] = true
	}
	// Your most recent review point.
	var latest store.FileMark
	for _, m := range marks {
		if m.MarkedAt.After(latest.MarkedAt) {
			latest = m
		}
	}
	if latest.HeadSHA != "" && latest.HeadSHA != pr.HeadRefOid {
		found := false
		for i := range out {
			if out[i].SHA == latest.HeadSHA {
				out[i].YourLast = true
				found = true
			}
		}
		if !found {
			out = append(out, revision{Label: "your last review", SHA: latest.HeadSHA, Short: short(latest.HeadSHA), Headline: "(no longer on the branch)", When: latest.MarkedAt, YourLast: true})
		}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// rangeFiles returns the changed files for base..head, using the pull
// request's own file list when the range is the whole PR. Results are cached:
// commit ranges are immutable, the PR list for a head expires after filesTTL.
func (s *Server) rangeFiles(ctx context.Context, client *gh.Client, key store.PRKey, pr *gh.PullRequest, base, head string) ([]gh.ChangedFile, error) {
	whole := base == "" && head == pr.HeadRefOid
	var ck string
	if whole {
		ck = fmt.Sprintf("%s/%s#%d@%s", key.Owner, key.Repo, key.Number, head)
	} else {
		if base == "" {
			base = pr.BaseRefOid
		}
		ck = key.Owner + "/" + key.Repo + ":" + base + ".." + head
	}
	s.filesMu.Lock()
	e, ok := s.filesCache[ck]
	s.filesMu.Unlock()
	if ok && (!whole || time.Since(e.at) < filesTTL) {
		return e.files, nil
	}
	var files []gh.ChangedFile
	var err error
	if whole {
		files, err = client.Files(ctx, key.Owner, key.Repo, key.Number)
	} else {
		files, err = client.Compare(ctx, key.Owner, key.Repo, base, head)
	}
	if err != nil {
		return nil, err
	}
	s.filesMu.Lock()
	if s.filesCache == nil || len(s.filesCache) > 256 {
		s.filesCache = map[string]filesEntry{}
	}
	s.filesCache[ck] = filesEntry{files: files, at: time.Now()}
	s.filesMu.Unlock()
	return files, nil
}

// changedBetween returns the set of paths that differ between two commits,
// cached per (repo, from, to).
func (s *Server) changedBetween(ctx context.Context, client *gh.Client, key store.PRKey, from, to string) (map[string]bool, error) {
	ck := key.Owner + "/" + key.Repo + ":" + from + ".." + to
	s.compareMu.Lock()
	if v, ok := s.compareCache[ck]; ok {
		s.compareMu.Unlock()
		return v, nil
	}
	s.compareMu.Unlock()
	files, err := client.Compare(ctx, key.Owner, key.Repo, from, to)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(files))
	for _, f := range files {
		set[f.Path] = true
		if f.PreviousPath != "" {
			set[f.PreviousPath] = true
		}
	}
	s.compareMu.Lock()
	if len(s.compareCache) > 512 {
		s.compareCache = map[string]map[string]bool{}
	}
	s.compareCache[ck] = set
	s.compareMu.Unlock()
	return set, nil
}

// buildFileDetail parses the patch and anchors threads, drafts and the
// composer to their lines.
func buildFileDetail(f gh.ChangedFile, threads []gh.Thread, drafts []store.Draft, comp composer) *fileDetail {
	det := &fileDetail{Path: f.Path, PreviousPath: f.PreviousPath, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
	parsed, err := diff.Parse(f.Patch)
	if err != nil || parsed.Truncated {
		det.Truncated = true
	}
	type anchor struct {
		side string
		line int
	}
	threadAt := map[anchor][]gh.Thread{}
	for _, t := range threads {
		if t.Line == nil {
			det.Outdated = append(det.Outdated, t)
			continue
		}
		side := t.DiffSide
		if side == "" {
			side = "RIGHT"
		}
		threadAt[anchor{side, *t.Line}] = append(threadAt[anchor{side, *t.Line}], t)
	}
	draftAt := map[anchor][]store.Draft{}
	for _, d := range drafts {
		draftAt[anchor{d.Side, d.Line}] = append(draftAt[anchor{d.Side, d.Line}], d)
	}
	compOpen := comp.Path == f.Path && comp.Line > 0
	selFrom, selTo := comp.StartLine, comp.Line
	if selFrom == 0 || selFrom > selTo {
		selFrom = selTo
	}
	if det.Truncated {
		return det
	}
	total := 0
	for _, h := range parsed.Hunks {
		if total > maxRenderedLines {
			det.TooLarge = true
			break
		}
		hv := hunkView{Header: h.Header}
		decorate := func(l diff.Line) *uline {
			ul := &uline{Line: l}
			switch l.Kind {
			case diff.Add:
				ul.Threads = threadAt[anchor{"RIGHT", l.NewNo}]
				ul.Drafts = draftAt[anchor{"RIGHT", l.NewNo}]
				ul.Composer = compOpen && comp.Side == "RIGHT" && comp.Line == l.NewNo
				ul.Selected = compOpen && comp.Side == "RIGHT" && l.NewNo >= selFrom && l.NewNo <= selTo
			case diff.Del:
				ul.Threads = threadAt[anchor{"LEFT", l.OldNo}]
				ul.Drafts = draftAt[anchor{"LEFT", l.OldNo}]
				ul.Composer = compOpen && comp.Side == "LEFT" && comp.Line == l.OldNo
				ul.Selected = compOpen && comp.Side == "LEFT" && l.OldNo >= selFrom && l.OldNo <= selTo
			case diff.Context:
				ul.Threads = append(threadAt[anchor{"RIGHT", l.NewNo}], threadAt[anchor{"LEFT", l.OldNo}]...)
				ul.Drafts = append(draftAt[anchor{"RIGHT", l.NewNo}], draftAt[anchor{"LEFT", l.OldNo}]...)
				ul.Composer = compOpen && ((comp.Side == "RIGHT" && comp.Line == l.NewNo) || (comp.Side == "LEFT" && comp.Line == l.OldNo))
				ul.Selected = compOpen && ((comp.Side == "RIGHT" && l.NewNo >= selFrom && l.NewNo <= selTo) || (comp.Side == "LEFT" && l.OldNo >= selFrom && l.OldNo <= selTo))
			}
			return ul
		}
		for _, l := range h.Lines {
			hv.Unified = append(hv.Unified, *decorate(l))
			total++
		}
		for _, row := range diff.SplitRows(h) {
			var r urow
			if row.Left != nil {
				r.Left = decorate(*row.Left)
				if row.Left.Kind == diff.Context {
					// Context lines carry right-side anchors on the right cell only.
					l := *r.Left
					l.Threads, l.Drafts = threadAt[anchor{"LEFT", l.OldNo}], draftAt[anchor{"LEFT", l.OldNo}]
					l.Composer = compOpen && comp.Side == "LEFT" && comp.Line == l.OldNo
					l.Selected = compOpen && comp.Side == "LEFT" && l.OldNo >= selFrom && l.OldNo <= selTo
					r.Left = &l
				}
			}
			if row.Right != nil {
				r.Right = decorate(*row.Right)
				if row.Right.Kind == diff.Context {
					l := *r.Right
					l.Threads, l.Drafts = threadAt[anchor{"RIGHT", l.NewNo}], draftAt[anchor{"RIGHT", l.NewNo}]
					l.Composer = compOpen && comp.Side == "RIGHT" && comp.Line == l.NewNo
					l.Selected = compOpen && comp.Side == "RIGHT" && l.NewNo >= selFrom && l.NewNo <= selTo
					r.Right = &l
				}
			}
			hv.Split = append(hv.Split, r)
		}
		det.Hunks = append(det.Hunks, hv)
	}
	return det
}
