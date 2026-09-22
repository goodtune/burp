package gh

import (
	"fmt"
	"net/http"

	ghub "github.com/google/go-github/v91/github"

	"github.com/goodtune/burp/internal/bus"
)

// Delivery is a parsed webhook delivery reduced to the topics it affects.
type Delivery struct {
	ID     string
	Event  string
	Action string
	Repo   string // owner/name
	// Topics are the bus topics to publish on.
	Topics []string
}

// ParseWebhook validates the signature and turns the payload into a
// Delivery. An empty secret disables validation (dev mode only).
func ParseWebhook(r *http.Request, secret string) (*Delivery, error) {
	payload, err := ghub.ValidatePayload(r, []byte(secret))
	if err != nil {
		return nil, fmt.Errorf("validating webhook: %w", err)
	}
	event := ghub.WebHookType(r)
	d := &Delivery{ID: ghub.DeliveryID(r), Event: event}
	parsed, err := ghub.ParseWebHook(event, payload)
	if err != nil {
		// Unknown event types are fine: acknowledge and ignore.
		return d, nil
	}
	seen := map[string]struct{}{}
	add := func(topics ...string) {
		for _, t := range topics {
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			d.Topics = append(d.Topics, t)
		}
	}
	pr := func(repo *ghub.Repository, p *ghub.PullRequest) {
		if repo == nil || p == nil {
			return
		}
		add(bus.PRTopic(repo.GetOwner().GetLogin(), repo.GetName(), p.GetNumber()))
		add(bus.RepoTopic(repo.GetOwner().GetLogin(), repo.GetName()))
		add(bus.UserTopic(p.GetUser().GetLogin()))
		for _, u := range p.RequestedReviewers {
			add(bus.UserTopic(u.GetLogin()))
		}
		for _, u := range p.Assignees {
			add(bus.UserTopic(u.GetLogin()))
		}
		if p.Head != nil {
			add(bus.SHATopic(p.Head.GetSHA()))
		}
	}

	switch e := parsed.(type) {
	case *ghub.PullRequestEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		pr(e.GetRepo(), e.GetPullRequest())
		add(bus.UserTopic(e.GetRequestedReviewer().GetLogin()))
		add(bus.UserTopic(e.GetSender().GetLogin()))
	case *ghub.PullRequestReviewEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		pr(e.GetRepo(), e.GetPullRequest())
		add(bus.UserTopic(e.GetReview().GetUser().GetLogin()))
	case *ghub.PullRequestReviewCommentEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		pr(e.GetRepo(), e.GetPullRequest())
		add(bus.UserTopic(e.GetComment().GetUser().GetLogin()))
	case *ghub.PullRequestReviewThreadEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		pr(e.GetRepo(), e.GetPullRequest())
	case *ghub.IssueCommentEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		if e.GetIssue().IsPullRequest() {
			add(bus.PRTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName(), e.GetIssue().GetNumber()))
			add(bus.RepoTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()))
			add(bus.UserTopic(e.GetIssue().GetUser().GetLogin()))
		}
	case *ghub.CheckRunEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		add(bus.RepoTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()))
		add(bus.SHATopic(e.GetCheckRun().GetHeadSHA()))
		for _, p := range e.GetCheckRun().PullRequests {
			add(bus.PRTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName(), p.GetNumber()))
		}
	case *ghub.CheckSuiteEvent:
		d.Action = e.GetAction()
		d.Repo = e.GetRepo().GetFullName()
		add(bus.RepoTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()))
		add(bus.SHATopic(e.GetCheckSuite().GetHeadSHA()))
		for _, p := range e.GetCheckSuite().PullRequests {
			add(bus.PRTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName(), p.GetNumber()))
		}
	case *ghub.StatusEvent:
		d.Repo = e.GetRepo().GetFullName()
		add(bus.RepoTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()))
		add(bus.SHATopic(e.GetSHA()))
	case *ghub.PushEvent:
		d.Repo = e.GetRepo().GetFullName()
		add(bus.RepoTopic(e.GetRepo().GetOwner().GetLogin(), e.GetRepo().GetName()))
	case *ghub.InstallationEvent:
		d.Action = e.GetAction()
	case *ghub.PingEvent:
	}
	return d, nil
}
