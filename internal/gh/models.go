package gh

import "time"

// Actor is a GitHub user as rendered in the UI.
type Actor struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// PRSummary is a pull request row in the inbox.
type PRSummary struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	State          string    `json:"state"` // OPEN, CLOSED, MERGED
	ReviewDecision string    `json:"reviewDecision"`
	UpdatedAt      time.Time `json:"updatedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changedFiles"`
	HeadRefOid     string    `json:"headRefOid"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Author  Actor `json:"author"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login string `json:"login"`
				Slug  string `json:"slug"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			Author Actor  `json:"author"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
}

// Owner and Repo split Repository.NameWithOwner.
func (p PRSummary) Owner() string { return splitRepo(p.Repository.NameWithOwner, 0) }
func (p PRSummary) Repo() string  { return splitRepo(p.Repository.NameWithOwner, 1) }

// CheckState is the status rollup for the head commit (SUCCESS, FAILURE,
// PENDING, ERROR, EXPECTED or "" when there are no checks).
func (p PRSummary) CheckState() string {
	if len(p.Commits.Nodes) == 0 || p.Commits.Nodes[0].Commit.StatusCheckRollup == nil {
		return ""
	}
	return p.Commits.Nodes[0].Commit.StatusCheckRollup.State
}

// RequestedReviewers lists requested user logins and team slugs.
func (p PRSummary) RequestedReviewers() []string {
	out := make([]string, 0, len(p.ReviewRequests.Nodes))
	for _, n := range p.ReviewRequests.Nodes {
		if n.RequestedReviewer.Login != "" {
			out = append(out, n.RequestedReviewer.Login)
		} else if n.RequestedReviewer.Slug != "" {
			out = append(out, n.RequestedReviewer.Slug)
		}
	}
	return out
}

func splitRepo(nwo string, i int) string {
	for j := 0; j < len(nwo); j++ {
		if nwo[j] == '/' {
			if i == 0 {
				return nwo[:j]
			}
			return nwo[j+1:]
		}
	}
	if i == 0 {
		return nwo
	}
	return ""
}

// Inbox holds the six inbox sections.
type Inbox struct {
	NeedsReview []PRSummary
	Returned    []PRSummary
	Approved    []PRSummary
	Waiting     []PRSummary
	Drafts      []PRSummary
	Merged      []PRSummary
	// Warnings are per-section errors (e.g. search rate limits).
	Warnings []string
}

// Repos lists every distinct repository in the inbox as owner/name.
func (in *Inbox) Repos() []string {
	seen := map[string]struct{}{}
	var out []string
	for _, sec := range [][]PRSummary{in.NeedsReview, in.Returned, in.Approved, in.Waiting, in.Drafts, in.Merged} {
		for _, p := range sec {
			if _, ok := seen[p.Repository.NameWithOwner]; ok {
				continue
			}
			seen[p.Repository.NameWithOwner] = struct{}{}
			out = append(out, p.Repository.NameWithOwner)
		}
	}
	return out
}

// PullRequest is the full review page model.
type PullRequest struct {
	ID               string    `json:"id"`
	Number           int       `json:"number"`
	Title            string    `json:"title"`
	BodyHTML         string    `json:"bodyHTML"`
	URL              string    `json:"url"`
	State            string    `json:"state"`
	IsDraft          bool      `json:"isDraft"`
	Merged           bool      `json:"merged"`
	Mergeable        string    `json:"mergeable"`        // MERGEABLE, CONFLICTING, UNKNOWN
	MergeStateStatus string    `json:"mergeStateStatus"` // CLEAN, BLOCKED, DIRTY, BEHIND, UNSTABLE, DRAFT, HAS_HOOKS, UNKNOWN
	ReviewDecision   string    `json:"reviewDecision"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	Additions        int       `json:"additions"`
	Deletions        int       `json:"deletions"`
	ChangedFiles     int       `json:"changedFiles"`
	BaseRefName      string    `json:"baseRefName"`
	HeadRefName      string    `json:"headRefName"`
	HeadRefOid       string    `json:"headRefOid"`
	BaseRefOid       string    `json:"baseRefOid"`
	ViewerCanUpdate  bool      `json:"viewerCanUpdate"`
	ViewerDidAuthor  bool      `json:"viewerDidAuthor"`
	Author           Actor     `json:"author"`
	Repository       struct {
		NameWithOwner      string `json:"nameWithOwner"`
		MergeCommitAllowed bool   `json:"mergeCommitAllowed"`
		SquashMergeAllowed bool   `json:"squashMergeAllowed"`
		RebaseMergeAllowed bool   `json:"rebaseMergeAllowed"`
		ViewerPermission   string `json:"viewerPermission"`
	} `json:"repository"`
	Labels struct {
		Nodes []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	Commits struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Commit Commit `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	HeadCommit struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						Nodes []CheckContext `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"headCommit"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login string `json:"login"`
				Slug  string `json:"slug"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []Review `json:"nodes"`
	} `json:"latestReviews"`
	Reviews struct {
		Nodes []Review `json:"nodes"`
	} `json:"reviews"`
	ReviewThreads struct {
		Nodes []Thread `json:"nodes"`
	} `json:"reviewThreads"`
	Comments struct {
		Nodes []Comment `json:"nodes"`
	} `json:"comments"`
}

// Commit is a PR commit (a "revision").
type Commit struct {
	Oid             string    `json:"oid"`
	AbbreviatedOid  string    `json:"abbreviatedOid"`
	MessageHeadline string    `json:"messageHeadline"`
	CommittedDate   time.Time `json:"committedDate"`
	Author          struct {
		Name string `json:"name"`
		User *Actor `json:"user"`
	} `json:"author"`
}

// CheckContext is a check run or commit status.
type CheckContext struct {
	Typename    string `json:"__typename"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	DetailsURL  string `json:"detailsUrl"`
	Title       string `json:"title"`
	Context     string `json:"context"`
	State       string `json:"state"`
	TargetURL   string `json:"targetUrl"`
	Description string `json:"description"`
	CheckSuite  *struct {
		App *struct {
			Name string `json:"name"`
		} `json:"app"`
	} `json:"checkSuite"`
}

// Label is the display name.
func (c CheckContext) Label() string {
	if c.Typename == "StatusContext" {
		return c.Context
	}
	if c.CheckSuite != nil && c.CheckSuite.App != nil && c.CheckSuite.App.Name != "" && c.CheckSuite.App.Name != "GitHub Actions" {
		return c.CheckSuite.App.Name + " / " + c.Name
	}
	return c.Name
}

// URL is the details link.
func (c CheckContext) URL() string {
	if c.Typename == "StatusContext" {
		return c.TargetURL
	}
	return c.DetailsURL
}

// Outcome normalises a check into success, failure, pending, neutral or
// skipped.
func (c CheckContext) Outcome() string {
	if c.Typename == "StatusContext" {
		switch c.State {
		case "SUCCESS":
			return "success"
		case "FAILURE", "ERROR":
			return "failure"
		default:
			return "pending"
		}
	}
	if c.Status != "COMPLETED" {
		return "pending"
	}
	switch c.Conclusion {
	case "SUCCESS":
		return "success"
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
		return "failure"
	case "SKIPPED":
		return "skipped"
	default:
		return "neutral"
	}
}

// Review is a submitted PR review.
type Review struct {
	ID          string    `json:"id"`
	DatabaseID  int64     `json:"databaseId"`
	Author      Actor     `json:"author"`
	State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	BodyHTML    string    `json:"bodyHTML"`
	SubmittedAt time.Time `json:"submittedAt"`
	URL         string    `json:"url"`
}

// Thread is a review thread with its comments.
type Thread struct {
	ID               string `json:"id"`
	IsResolved       bool   `json:"isResolved"`
	IsOutdated       bool   `json:"isOutdated"`
	ViewerCanResolve bool   `json:"viewerCanResolve"`
	Path             string `json:"path"`
	Line             *int   `json:"line"`
	StartLine        *int   `json:"startLine"`
	OriginalLine     *int   `json:"originalLine"`
	DiffSide         string `json:"diffSide"`
	ResolvedBy       *Actor `json:"resolvedBy"`
	Comments         struct {
		Nodes []Comment `json:"nodes"`
	} `json:"comments"`
}

// AnchorLine is the line the thread is shown at (line, else originalLine).
func (t Thread) AnchorLine() int {
	if t.Line != nil {
		return *t.Line
	}
	if t.OriginalLine != nil {
		return *t.OriginalLine
	}
	return 0
}

// Comment is a review or issue comment.
type Comment struct {
	ID         string    `json:"id"`
	DatabaseID int64     `json:"databaseId"`
	Author     Actor     `json:"author"`
	Body       string    `json:"body"`
	BodyHTML   string    `json:"bodyHTML"`
	CreatedAt  time.Time `json:"createdAt"`
	URL        string    `json:"url"`
}

// Owner and Repo split the repository name.
func (p *PullRequest) Owner() string { return splitRepo(p.Repository.NameWithOwner, 0) }
func (p *PullRequest) Repo() string  { return splitRepo(p.Repository.NameWithOwner, 1) }

// Checks returns the head commit's check contexts and rollup state.
func (p *PullRequest) Checks() (state string, contexts []CheckContext) {
	if len(p.HeadCommit.Nodes) == 0 || p.HeadCommit.Nodes[0].Commit.StatusCheckRollup == nil {
		return "", nil
	}
	r := p.HeadCommit.Nodes[0].Commit.StatusCheckRollup
	return r.State, r.Contexts.Nodes
}

// RequestedReviewers lists requested user logins and team slugs.
func (p *PullRequest) RequestedReviewers() []string {
	out := make([]string, 0, len(p.ReviewRequests.Nodes))
	for _, n := range p.ReviewRequests.Nodes {
		if n.RequestedReviewer.Login != "" {
			out = append(out, n.RequestedReviewer.Login)
		} else if n.RequestedReviewer.Slug != "" {
			out = append(out, n.RequestedReviewer.Slug)
		}
	}
	return out
}

// UnresolvedThreads counts open threads.
func (p *PullRequest) UnresolvedThreads() (unresolved, total int) {
	for _, t := range p.ReviewThreads.Nodes {
		total++
		if !t.IsResolved {
			unresolved++
		}
	}
	return unresolved, total
}

// ChangedFile is a file from the pull request files or compare endpoints.
type ChangedFile struct {
	Path         string
	PreviousPath string
	Status       string // added, removed, modified, renamed, copied, changed
	Additions    int
	Deletions    int
	Patch        string
	SHA          string
}

// Installation is a GitHub App installation visible to the user.
type Installation struct {
	ID          int64
	Account     string
	AccountType string
	HTMLURL     string
	Selection   string
}

// Viewer is the signed-in GitHub user.
type Viewer struct {
	ID        int64
	Login     string
	AvatarURL string
}
