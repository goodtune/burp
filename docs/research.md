# Research: what Graphite and Reviewable do for GitHub PR review

This note summarises the features of two prominent third-party GitHub review
tools and records which of them burp adopts. It was compiled before the
wireframes and the implementation. Sources are the products' public docs and
changelogs (September 2026).

## Graphite

* **PR inbox** – "an email client for PRs". Default sections: *Needs your
  review*, *Approved*, *Returned to you*, *Merging and recently merged*,
  *Drafts*, *Waiting for review*. Custom sections are saved filters (author,
  CI status, review state, labels, age, repo). Sort by title, size, or last
  updated. Data is kept current through GitHub webhooks, not polling.
  (graphite.com/docs/use-pr-inbox)
* **Review page** – split diff by default with a unified toggle; file tree on
  the left (`F`), focus mode hides it. Every update to a PR creates a
  *version* (`v1, v2, …`); a Compare dropdown (`V`) diffs any two versions,
  and after you have reviewed a PR "hide reviewed changes" shows only the
  delta since your last review. Comments can be attached to any line, single
  or multi-line, and are batched into a pending review ("Add to review") with
  the pending count in the header and a "Finish review" action.
  (graphite.com/docs/review-pull-requests, graphite.com/docs/pull-request-versions)
* **Metadata panel** – PR status, review status, expandable CI checks with
  logs, reviewers, labels, assignees. *Action cards* tell the user the single
  blocking issue (failing check, missing approval, conflict) with a one-click
  next step.
* **Keyboard** – `F` file tree, `V` versions, `R C` comment, `R N` request
  changes, `R A` approve, `R Y` quick approve.
* **Stacks and merge queue** – stack-aware merging and a merge queue that
  validates a whole stack; distinctive to Graphite and coupled to its CLI.
* **Integration** – a GitHub App with pull request, checks, contents and
  metadata permissions, driven by webhook subscriptions.

## Reviewable

* **Dashboard** – open PRs where you are author, assignee, reviewer or
  mentioned, grouped by "who must act" with *Awaiting my action* first;
  participant avatars decorated with status; stalled (>2 weeks) markers; a
  Merge button when everything is green. Structured search filters
  (`+needs:review`, `+am:author`, `+red`). Refreshes about once a minute.
  (docs.reviewable.io/dashboard.html)
* **Revisions** – every push is a revision `r1, r2, …`; diff bounds are any
  two revisions (or the base), with virtual markers for "last revision
  reviewed by you". Force-pushed revisions are kept and remain diffable.
  (docs.reviewable.io/files.html)
* **Per-file review marks** – each file has a reviewed mark *per revision*. A
  new revision resets the file to unreviewed until re-marked, and the
  incremental diff shows only what you have not seen. Author self-marks are
  distinguished. A "review chip" summarises who has reviewed each file at
  the latest revision. `n` cycles unreviewed files.
* **Discussions** – comments on any line persist across revisions and are
  mapped to corresponding lines. Each participant holds a *disposition*
  (Discussing, Blocking, Working, Satisfied, Informing); a discussion is
  resolved when someone is Satisfied and nobody is Blocking or Working.
  (docs.reviewable.io/discussions.html)
* **Drafts and publish** – comments, marks and dispositions are drafts saved
  continuously and published together as one GitHub review (Comment /
  Approve / Request changes). An LGTM button approves quickly.
  (docs.reviewable.io/reviews.html)
* **Completion** – a donut of required/optional checks plus counters for
  "you must act" / "others must act". Default completion: no conflicts, every
  file reviewed at the latest revision by a non-author, all discussions
  resolved. Optionally a scripted completion condition published as a commit
  status.
* **Integration** – an OAuth app plus webhooks for `pull_request`, `push`,
  comments, `check_run` and `workflow_job`; reviews are published through the
  GitHub Reviews API; thread resolution is synced from GitHub.

## What burp takes from each

| Feature                                          | Source     | burp |
|--------------------------------------------------|------------|------|
| Sectioned inbox ordered by "what needs me next"  | Graphite   | yes, six sections driven by GitHub search |
| Live updates through webhooks, no polling        | Graphite   | yes, SSE per page fed by an in-process event bus |
| Split / unified diff, file tree, `j`/`k` navigation | both    | yes |
| Per-file reviewed marks recorded per revision    | Reviewable | yes, stored by burp keyed by head SHA |
| Revision (interdiff) comparison                  | both       | yes, via the GitHub compare API |
| "Only what changed since I last reviewed"        | both       | yes, derived from the newest reviewed mark |
| Draft comments batched into one GitHub review    | both       | yes, drafts stored by burp; submit posts one review |
| Approve / request changes / comment, quick approve | both     | yes |
| Thread resolve / unresolve synced with GitHub    | both       | yes, GraphQL `resolveReviewThread` |
| CI rollup and per-check list                     | both       | yes, GraphQL `statusCheckRollup` |
| Action card: the one thing blocking merge        | Graphite   | yes, computed from review decision, mergeability and checks |
| Merge from the tool                              | both       | yes, when the PR is mergeable and the user has push rights |
| Participant dispositions, scripted completion    | Reviewable | no, out of scope (GitHub's review states are the model) |
| Stacked PRs and merge queue                      | Graphite   | no, requires a CLI and server-side rebasing |
| AI review                                        | Graphite   | no |
| Slack notifications                              | both       | no, later |

## Datastar (frontend transport)

Datastar (data-star.dev, v1.0.x) is a small hypermedia library: HTML carries
`data-*` attributes (`data-signals`, `data-bind`, `data-on:click`,
`data-init`, `data-show`, `data-class`, `data-indicator`) and actions
(`@get`, `@post`, `@patch`, `@delete`) that call the backend. The backend
answers with Server-Sent Events carrying `datastar-patch-elements` (HTML
fragments merged into the DOM by `id`, with `mode` outer / inner / append /
prepend / remove) and `datastar-patch-signals` (JSON merged into client
signals). A `@get` whose response never closes becomes a long-lived push
channel, which is how burp delivers webhook-driven updates. The official Go
SDK is `github.com/starfederation/datastar-go/datastar` (`NewSSE`,
`PatchElements`, `PatchSignals`, `ReadSignals`). No other client-side
JavaScript is used; the datastar bundle is vendored and served by burp.
