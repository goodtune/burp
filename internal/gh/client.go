package gh

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ghub "github.com/google/go-github/v91/github"
)

// Client calls GitHub as one user.
type Client struct {
	token      string
	http       *http.Client
	rest       *ghub.Client
	graphqlURL string
}

// ClientFactory builds Clients with the configured endpoints.
type ClientFactory struct {
	APIURL     string
	GraphQLURL string
	HTTPClient *http.Client
}

// New returns a Client authenticated with token.
func (f *ClientFactory) New(token string) *Client {
	hc := f.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	opts := []ghub.ClientOptionsFunc{ghub.WithHTTPClient(hc), ghub.WithAuthToken(token)}
	if f.APIURL != "" && f.APIURL != "https://api.github.com" {
		opts = append(opts, ghub.WithEnterpriseURLs(f.APIURL, f.APIURL))
	}
	// NewClient only fails on a malformed enterprise URL, which config
	// validation already rejects; fall back to defaults defensively.
	rest, err := ghub.NewClient(opts...)
	if err != nil {
		rest, _ = ghub.NewClient(ghub.WithHTTPClient(hc), ghub.WithAuthToken(token))
	}
	return &Client{token: token, http: hc, rest: rest, graphqlURL: f.GraphQLURL}
}

// restErr maps go-github errors to burp errors.
func restErr(err error) error {
	if err == nil {
		return nil
	}
	var er *ghub.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		if er.Response.StatusCode == http.StatusUnauthorized {
			return ErrReauthRequired
		}
		msg := er.Message
		for _, e := range er.Errors {
			if e.Message != "" {
				msg += "; " + e.Message
			}
		}
		return &APIError{Status: er.Response.StatusCode, Message: msg}
	}
	return err
}

// Viewer returns the authenticated user.
func (c *Client) Viewer(ctx context.Context) (*Viewer, error) {
	u, _, err := c.rest.Users.Get(ctx, "")
	if err != nil {
		return nil, restErr(err)
	}
	return &Viewer{ID: u.GetID(), Login: u.GetLogin(), AvatarURL: u.GetAvatarURL()}, nil
}

const prSummaryFragment = `
fragment PRSummary on PullRequest {
  number title url isDraft state reviewDecision updatedAt createdAt additions deletions changedFiles headRefOid
  repository { nameWithOwner }
  author { login avatarUrl }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
  reviewRequests(first: 10) { nodes { requestedReviewer { ... on User { login } ... on Team { slug } } } }
  latestReviews(first: 10) { nodes { author { login avatarUrl } state } }
}`

const inboxQuery = `
query Inbox($needsReview: String!, $returned: String!, $approved: String!, $waiting: String!, $drafts: String!, $merged: String!) {
  needsReview: search(type: ISSUE, query: $needsReview, first: 30) { nodes { ...PRSummary } }
  returned: search(type: ISSUE, query: $returned, first: 30) { nodes { ...PRSummary } }
  approved: search(type: ISSUE, query: $approved, first: 30) { nodes { ...PRSummary } }
  waiting: search(type: ISSUE, query: $waiting, first: 30) { nodes { ...PRSummary } }
  drafts: search(type: ISSUE, query: $drafts, first: 30) { nodes { ...PRSummary } }
  merged: search(type: ISSUE, query: $merged, first: 10) { nodes { ...PRSummary } }
}` + prSummaryFragment

// InboxQueries are the GitHub search strings behind each section; the
// optional filter (e.g. "repo:acme/api") is appended to each.
func InboxQueries(filter string) map[string]string {
	f := strings.TrimSpace(filter)
	if f != "" {
		f = " " + f
	}
	return map[string]string{
		"needsReview": "is:pr is:open review-requested:@me sort:updated-desc" + f,
		"returned":    "is:pr is:open author:@me review:changes_requested sort:updated-desc" + f,
		"approved":    "is:pr is:open author:@me review:approved sort:updated-desc" + f,
		"waiting":     "is:pr is:open author:@me draft:false -review:approved -review:changes_requested sort:updated-desc" + f,
		"drafts":      "is:pr is:open author:@me draft:true sort:updated-desc" + f,
		"merged":      "is:pr is:merged involves:@me sort:updated-desc" + f,
	}
}

// Inbox runs the six searches as the user.
func (c *Client) Inbox(ctx context.Context, filter string) (*Inbox, error) {
	vars := map[string]any{}
	for k, v := range InboxQueries(filter) {
		vars[k] = v
	}
	type section struct {
		Nodes []PRSummary `json:"nodes"`
	}
	var data struct {
		NeedsReview section `json:"needsReview"`
		Returned    section `json:"returned"`
		Approved    section `json:"approved"`
		Waiting     section `json:"waiting"`
		Drafts      section `json:"drafts"`
		Merged      section `json:"merged"`
	}
	err := c.graphql(ctx, inboxQuery, vars, &data)
	in := &Inbox{
		NeedsReview: onlyPRs(data.NeedsReview.Nodes),
		Returned:    onlyPRs(data.Returned.Nodes),
		Approved:    onlyPRs(data.Approved.Nodes),
		Waiting:     onlyPRs(data.Waiting.Nodes),
		Drafts:      onlyPRs(data.Drafts.Nodes),
		Merged:      onlyPRs(data.Merged.Nodes),
	}
	if err != nil {
		var ge GraphQLErrors
		if errors.As(err, &ge) {
			for _, e := range ge {
				in.Warnings = append(in.Warnings, e.Message)
			}
			return in, nil
		}
		return nil, err
	}
	return in, nil
}

// onlyPRs drops issues that a search could return and empty nodes.
func onlyPRs(in []PRSummary) []PRSummary {
	out := make([]PRSummary, 0, len(in))
	for _, p := range in {
		if p.Number == 0 || p.Repository.NameWithOwner == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

const pullRequestQuery = `
query PR($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      id number title bodyHTML url state isDraft merged mergeable mergeStateStatus reviewDecision
      createdAt updatedAt additions deletions changedFiles
      baseRefName headRefName headRefOid baseRefOid viewerCanUpdate viewerDidAuthor
      author { login avatarUrl }
      repository { nameWithOwner mergeCommitAllowed squashMergeAllowed rebaseMergeAllowed viewerPermission }
      labels(first: 20) { nodes { name color } }
      commits(first: 250) { totalCount nodes { commit { oid abbreviatedOid messageHeadline committedDate author { name user { login avatarUrl } } } } }
      headCommit: commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(first: 100) { nodes {
        __typename
        ... on CheckRun { name status conclusion detailsUrl title checkSuite { app { name } } }
        ... on StatusContext { context state targetUrl description }
      } } } } } }
      reviewRequests(first: 20) { nodes { requestedReviewer { ... on User { login } ... on Team { slug } } } }
      latestReviews(first: 20) { nodes { id databaseId author { login avatarUrl } state bodyHTML submittedAt url } }
      reviews(last: 50) { nodes { id databaseId author { login avatarUrl } state bodyHTML submittedAt url } }
      reviewThreads(first: 100) { nodes {
        id isResolved isOutdated viewerCanResolve path line startLine originalLine diffSide resolvedBy { login avatarUrl }
        comments(first: 100) { nodes { id databaseId author { login avatarUrl } body bodyHTML createdAt url } }
      } }
      comments(last: 50) { nodes { id databaseId author { login avatarUrl } body bodyHTML createdAt url } }
      files(first: 100) { pageInfo { hasNextPage endCursor } nodes { path viewerViewedState } }
    }
  }
}`

// filesPageQuery continues the per-file viewed state for large pull requests.
const filesPageQuery = `
query Files($id: ID!, $after: String!) {
  node(id: $id) { ... on PullRequest {
    files(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { path viewerViewedState } }
  } }
}`

// PullRequest loads the review page model.
func (c *Client) PullRequest(ctx context.Context, owner, repo string, number int) (*PullRequest, error) {
	var data struct {
		Repository *struct {
			PullRequest *PullRequest `json:"pullRequest"`
		} `json:"repository"`
	}
	err := c.graphql(ctx, pullRequestQuery, map[string]any{"owner": owner, "name": repo, "number": number}, &data)
	if err != nil {
		var ge GraphQLErrors
		if !errors.As(err, &ge) || data.Repository == nil || data.Repository.PullRequest == nil {
			if errors.As(err, &ge) {
				return nil, &APIError{Status: http.StatusNotFound, Message: ge.Error()}
			}
			return nil, err
		}
	}
	if data.Repository == nil || data.Repository.PullRequest == nil {
		return nil, &APIError{Status: http.StatusNotFound, Message: "pull request not found"}
	}
	pr := data.Repository.PullRequest
	for i := 0; pr.Files.PageInfo.HasNextPage && i < 30; i++ {
		var page struct {
			Node struct {
				Files FileConnection `json:"files"`
			} `json:"node"`
		}
		if err := c.graphql(ctx, filesPageQuery, map[string]any{"id": pr.ID, "after": pr.Files.PageInfo.EndCursor}, &page); err != nil {
			break // viewed state is best effort
		}
		pr.Files.Nodes = append(pr.Files.Nodes, page.Node.Files.Nodes...)
		pr.Files.PageInfo = page.Node.Files.PageInfo
	}
	return pr, nil
}

// MarkFileViewed sets or clears GitHub's own "viewed" state for a file on
// the pull request, so that burp's reviewed marks show up on github.com.
func (c *Client) MarkFileViewed(ctx context.Context, prNodeID, path string, viewed bool) error {
	mutation := `mutation($id: ID!, $path: String!) { markFileAsViewed(input: {pullRequestId: $id, path: $path}) { pullRequest { id } } }`
	if !viewed {
		mutation = `mutation($id: ID!, $path: String!) { unmarkFileAsViewed(input: {pullRequestId: $id, path: $path}) { pullRequest { id } } }`
	}
	return c.graphql(ctx, mutation, map[string]any{"id": prNodeID, "path": path}, nil)
}

// Files lists the changed files of the pull request with patches.
func (c *Client) Files(ctx context.Context, owner, repo string, number int) ([]ChangedFile, error) {
	var out []ChangedFile
	opts := &ghub.ListOptions{PerPage: 100}
	for {
		files, resp, err := c.rest.PullRequests.ListFiles(ctx, owner, repo, number, opts)
		if err != nil {
			return nil, restErr(err)
		}
		for _, f := range files {
			out = append(out, toChangedFile(f))
		}
		if resp.NextPage == 0 || len(out) >= 3000 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func toChangedFile(f *ghub.CommitFile) ChangedFile {
	return ChangedFile{
		Path:         f.GetFilename(),
		PreviousPath: f.GetPreviousFilename(),
		Status:       f.GetStatus(),
		Additions:    f.GetAdditions(),
		Deletions:    f.GetDeletions(),
		Patch:        f.GetPatch(),
		SHA:          f.GetSHA(),
	}
}

// Compare returns the files changed between two commits (base...head).
func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) ([]ChangedFile, error) {
	var out []ChangedFile
	opts := &ghub.ListOptions{PerPage: 100}
	for {
		cmp, resp, err := c.rest.Repositories.CompareCommits(ctx, owner, repo, base, head, opts)
		if err != nil {
			return nil, restErr(err)
		}
		for _, f := range cmp.Files {
			out = append(out, toChangedFile(f))
		}
		if resp.NextPage == 0 || len(out) >= 3000 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

// ReviewComment is a line comment submitted with a review.
type ReviewComment struct {
	Path      string
	Body      string
	Line      int
	Side      string
	StartLine int
}

// SubmitReview posts one review with the given comments. event is
// APPROVE, REQUEST_CHANGES or COMMENT.
func (c *Client) SubmitReview(ctx context.Context, owner, repo string, number int, commitID, event, body string, comments []ReviewComment) error {
	req := &ghub.PullRequestReviewRequest{
		CommitID: ghub.Ptr(commitID),
		Event:    ghub.Ptr(event),
	}
	if body != "" {
		req.Body = ghub.Ptr(body)
	}
	for _, cm := range comments {
		d := &ghub.DraftReviewComment{
			Path: ghub.Ptr(cm.Path),
			Body: ghub.Ptr(cm.Body),
			Line: ghub.Ptr(cm.Line),
			Side: ghub.Ptr(cm.Side),
		}
		if cm.StartLine > 0 && cm.StartLine < cm.Line {
			d.StartLine = ghub.Ptr(cm.StartLine)
			d.StartSide = ghub.Ptr(cm.Side)
		}
		req.Comments = append(req.Comments, d)
	}
	_, _, err := c.rest.PullRequests.CreateReview(ctx, owner, repo, number, req)
	return restErr(err)
}

// Reply posts a reply in an existing review thread.
func (c *Client) Reply(ctx context.Context, owner, repo string, number int, commentID int64, body string) error {
	_, _, err := c.rest.PullRequests.CreateCommentInReplyTo(ctx, owner, repo, number, body, commentID)
	return restErr(err)
}

// IssueComment posts a conversation comment on the pull request.
func (c *Client) IssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	_, _, err := c.rest.Issues.CreateComment(ctx, owner, repo, number, ghub.IssueCommentRequest{Body: body})
	return restErr(err)
}

// ResolveThread resolves or unresolves a review thread by node id.
func (c *Client) ResolveThread(ctx context.Context, threadID string, resolve bool) error {
	mutation := `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id isResolved } } }`
	if !resolve {
		mutation = `mutation($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id isResolved } } }`
	}
	return c.graphql(ctx, mutation, map[string]any{"id": threadID}, nil)
}

// Merge merges the pull request. method is merge, squash or rebase.
func (c *Client) Merge(ctx context.Context, owner, repo string, number int, method, headSHA string) error {
	_, _, err := c.rest.PullRequests.Merge(ctx, owner, repo, number, "", &ghub.PullRequestOptions{MergeMethod: method, SHA: headSHA})
	return restErr(err)
}

// Installations lists App installations the user can access.
func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	insts, _, err := c.rest.Apps.ListUserInstallations(ctx, &ghub.ListOptions{PerPage: 100})
	if err != nil {
		return nil, restErr(err)
	}
	out := make([]Installation, 0, len(insts))
	for _, i := range insts {
		out = append(out, Installation{
			ID:          i.GetID(),
			Account:     i.GetAccount().GetLogin(),
			AccountType: i.GetAccount().GetType(),
			HTMLURL:     i.GetHTMLURL(),
			Selection:   i.GetRepositorySelection(),
		})
	}
	return out, nil
}

// RequestReviewers asks the given users for review.
func (c *Client) RequestReviewers(ctx context.Context, owner, repo string, number int, logins []string) error {
	_, _, err := c.rest.PullRequests.RequestReviewers(ctx, owner, repo, number, ghub.ReviewersRequest{Reviewers: logins})
	return restErr(err)
}

// Ready marks a draft pull request ready for review.
func (c *Client) Ready(ctx context.Context, prNodeID string) error {
	return c.graphql(ctx, `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id } } }`, map[string]any{"id": prNodeID}, nil)
}

// String helper for error messages.
func prRef(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}
