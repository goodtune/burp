# burp — UX wireframes

These wireframes were produced before any implementation work and define the
user experience that the code in this repository implements. They are text
wireframes so that they can be reviewed, diffed and kept in sync with the
templates in `internal/web/templates/`.

Conventions:

* `[Button]` is an action, `<link>` is navigation, `( )`/`(•)` are radio
  options, `[ ]`/`[x]` are checkboxes.
* `⟳` marks a region that is re-rendered live over the page's Server-Sent
  Events (SSE) connection when a GitHub webhook for the shown PR (or the
  signed-in user) arrives. Nothing on the page polls.
* Every region is a plain server-rendered HTML fragment patched by datastar.
  There is no client-side application state beyond datastar signals.

---

## 1. Sign in (`/`)

Shown to anonymous users. The app is a GitHub App; signing in authorises the
app to act as the user (user-to-server token). Installing the app on a
repository is a separate, optional step that enables webhooks for that repo.

```
┌──────────────────────────────────────────────────────────────────────┐
│ burp                                                                 │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│                    Review GitHub pull requests,                      │
│                    without losing your place.                        │
│                                                                      │
│               [ Sign in with GitHub ]                                │
│                                                                      │
│   burp acts on GitHub as you. Reviews, comments and approvals you    │
│   submit here appear on GitHub under your own account.               │
│                                                                      │
│   Not installed on your repositories yet?  <Install the GitHub App>  │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

Flow: `[Sign in]` → GitHub authorize page → `/auth/callback` → `/inbox`.
`<Install the GitHub App>` → `https://github.com/apps/<slug>/installations/new`
→ GitHub redirects back to `/auth/callback?installation_id=…&setup_action=…`.

---

## 2. Inbox (`/inbox`)

The inbox is modelled on Graphite's sectioned PR inbox. Sections are ordered
by "what needs *me* next", newest activity first within a section. The whole
list is live: a webhook for any PR the user is involved in re-renders the
affected section.

```
┌──────────────────────────────────────────────────────────────────────┐
│ burp   <Inbox>   <Settings>                       @octocat ▾ [Sign out]│
├──────────────────────────────────────────────────────────────────────┤
│ Inbox                                          ⟳ updated 3s ago     │
│                                                                      │
│ Filter: [ repo:acme/api        ] (•) All ( ) Non-draft ( ) Mine only  │
│                                                                      │
│ ▼ Needs your review (2) ⟳                                            │
│ ┌────────────────────────────────────────────────────────────────┐   │
│ │ ● acme/api #412  Add rate limiting to /tokens        ✔ CI      │   │
│ │   @alice · 2h ago · +120 −14 · 6 files · review requested      │   │
│ │   ▮▮▮▯ 3/6 files reviewed by you                                │   │
│ ├────────────────────────────────────────────────────────────────┤   │
│ │ ● acme/web #98   Fix flaky login e2e                 ✖ CI      │   │
│ │   @bob · 1d ago · +8 −2 · 1 file · re-requested after changes  │   │
│ └────────────────────────────────────────────────────────────────┘   │
│                                                                      │
│ ▼ Returned to you (1) ⟳         (your PRs with changes requested)   │
│ ┌────────────────────────────────────────────────────────────────┐   │
│ │ ● acme/api #401  Migrate sessions table              ✔ CI      │   │
│ │   you · 3d ago · changes requested by @carol · 2 unresolved    │   │
│ └────────────────────────────────────────────────────────────────┘   │
│                                                                      │
│ ▼ Approved, ready to merge (1) ⟳                                     │
│ ┌────────────────────────────────────────────────────────────────┐   │
│ │ ● acme/api #399  Bump go-github                      ✔ CI      │   │
│ │   you · 5h ago · approved by @carol · mergeable                │   │
│ └────────────────────────────────────────────────────────────────┘   │
│                                                                      │
│ ▶ Waiting on reviewers (3)                                           │
│ ▶ Drafts (1)                                                         │
│ ▶ Recently merged (10)                                               │
└──────────────────────────────────────────────────────────────────────┘
```

Row anatomy: status dot (open / draft / merged / closed), `owner/repo #n`,
title (link to review page), CI rollup (✔ ✖ ● pending, ○ none), author,
relative time, size, file count, a one-line "why is this here" reason, and
for PRs the user is reviewing, a per-file review progress bar (Reviewable's
"files reviewed" concept, stored by burp, not GitHub).

Sections and the GitHub search behind them:

| Section                  | Query (as the signed-in user)                                  |
|--------------------------|----------------------------------------------------------------|
| Needs your review        | `is:pr is:open review-requested:@me`                           |
| Returned to you          | `is:pr is:open author:@me review:changes_requested`            |
| Approved, ready to merge | `is:pr is:open author:@me review:approved`                     |
| Waiting on reviewers     | `is:pr is:open author:@me -review:approved -review:changes_requested draft:false` |
| Drafts                   | `is:pr is:open author:@me draft:true`                          |
| Recently merged          | `is:pr is:merged involves:@me` (last 10)                       |

---

## 3. Pull request review page (`/pr/{owner}/{repo}/{number}`)

Three-pane layout: header, file navigator, diff + conversation. All `⟳`
regions update live when GitHub sends `pull_request`, `pull_request_review`,
`pull_request_review_comment`, `pull_request_review_thread`, `check_run`,
`check_suite` or `status` webhooks for this PR.

```
┌──────────────────────────────────────────────────────────────────────┐
│ burp   <Inbox>   <Settings>                       @octocat ▾ [Sign out]│
├──────────────────────────────────────────────────────────────────────┤
│ acme/api #412  Add rate limiting to /tokens                    ⟳     │
│ ● Open · @alice wants to merge  feat/ratelimit → main  · +120 −14    │
│                                                                      │
│ Checks ⟳  ✔ build  ✔ unit  ✖ e2e (failed, 2m)  ● lint (running)      │
│ Reviews ⟳ @carol ✔ approved · @you ● review requested · @dave 💬      │
│ Threads ⟳ 4 unresolved / 7                                           │
│                                                                      │
│ Revision:  base ▾ [ r1 (a1b2c3) ] … [ r3 (d4e5f6, latest) ▾ ]        │
│            ( ) whole PR   (•) changes since r2   ⟳ new revision r4!   │
│                                                                      │
│ View: (•) unified ( ) split   [x] hide whitespace  [x] show reviewed  │
│                                                                      │
│ ┌─ Blocking merge ⟳ ─────────────────────────────────────────────────┐ │
│ │ ✖ e2e failed · <view on GitHub>   ·  1 approval still required     │ │
│ └────────────────────────────────────────────────────────────────────┘ │
│                     [ Merge ▾ ]  [ Review ▾ ]  (3 draft comments)      │
├────────────────────┬─────────────────────────────────────────────────┤
│ Files 3/6 ⟳        │ internal/proxy/ratelimit.go          +80 −0  ⟳  │
│                    │ [x] Reviewed at r3   ← >  <prev file  next file> │
│ [x] go.mod         │─────────────────────────────────────────────────│
│ [x] go.sum         │ @@ -0,0 +1,80 @@                                │
│ [x] internal/      │  1 │ +package proxy                             │
│      config.go     │  2 │ +                                          │
│ [ ] internal/      │  3 │ +import (                                  │
│      proxy/        │  4 │ +    "net/http"                            │
│      ratelimit.go◀ │  5 │ +    "time"                                │
│ [ ] …ratelimit_    │  6 │ +)                                         │
│      test.go       │    │ ┌─ @carol · resolved ✔ ─────────────────┐  │
│ [ ] docs/          │    │ │ Consider golang.org/x/time/rate here.  │  │
│      config.md     │    │ │   @alice: done in r2                   │  │
│                    │    │ │ [Unresolve]  [Reply]                   │  │
│ ─────────────────  │    │ └────────────────────────────────────────┘  │
│ Legend             │  7 │ +type limiter struct {                     │
│ [x] reviewed       │    │ ┌─ @you · draft ✎ ────────────────────────┐ │
│  !  changed since  │    │ │ Should this be exported?               │ │
│     you reviewed   │    │ │ [Edit] [Delete]                        │ │
│                    │    │ └────────────────────────────────────────┘ │
│ Keyboard (?)       │  8 │ +    mu sync.Mutex                        │
│  j/k  next/prev   │    │        ← click a line number to comment    │
│       file         │    │ ┌─ New comment on line 8 ─────────────────┐│
│  n    next         │    │ │ [                                     ] ││
│       unreviewed   │    │ │ [Add to review]  [Cancel]              ││
│  r    mark         │    │ └────────────────────────────────────────┘│
│       reviewed     │                                                 │
│  v    revisions    │                                                 │
│  a    approve      │                                                 │
│                    │                                                 │
├────────────────────┴─────────────────────────────────────────────────┤
│ Conversation ⟳                                                       │
│ @alice opened · @carol approved 1h ago · @dave commented "LGTM but…" │
│ [ General comment                                          ] [Post]  │
└──────────────────────────────────────────────────────────────────────┘
```

### 3.1 Review submission dialog (`[Review ▾]`)

Comments made on the diff are **drafts held by burp** until the review is
submitted, so a reviewer can batch feedback the way Reviewable and GitHub's
own pending reviews do. Submitting posts a single GitHub review containing
every draft.

```
┌─ Submit review ─────────────────────────────────────────────┐
│ 3 draft comments will be posted with this review.           │
│                                                             │
│ Summary                                                     │
│ [                                                         ] │
│ [                                                         ] │
│                                                             │
│ ( ) Comment            — feedback without explicit approval │
│ (•) Approve            — this PR is ready to merge          │
│ ( ) Request changes    — must be addressed before merging   │
│                                                             │
│                        [Cancel]   [Submit review]           │
└─────────────────────────────────────────────────────────────┘
```

Rules: a user cannot approve or request changes on their own PR (GitHub
rejects it; burp hides the options). After submission drafts are cleared and
the review/threads regions re-render.

### 3.2 Revision comparison

Every push to the PR head is a *revision* (Reviewable's model). The revision
picker lets a reviewer see either the whole PR (base…head) or only what
changed between two revisions (interdiff, via GitHub's compare API between
the two head commits). "Reviewed" marks are recorded against the head commit
SHA at the time of marking. When a newer revision changes a file the user has
already reviewed, the file tree shows `!` and the mark is cleared (the mark
is kept in history so the "changes since you reviewed" mode can pick the
right base automatically).

```
Revision:  [ r1 a1b2c3 ] [ r2 b2c3d4 ] [ r3 d4e5f6 ]  ⟳ (r4 arrived: <reload>)
           base ▾ r2         head ▾ r3
```

### 3.3 Threads

Threads mirror GitHub review threads (GraphQL `reviewThreads`). Each thread
shows resolved/unresolved and outdated state, with `[Resolve]`/`[Unresolve]`
(GraphQL mutations) and `[Reply]` (posts an immediate reply comment on
GitHub, not a draft). Draft comments from burp render in the same position
with a ✎ badge and are only visible to their author.

---

## 4. Settings (`/settings`)

```
┌──────────────────────────────────────────────────────────────────────┐
│ burp   <Inbox>   <Settings>                       @octocat ▾ [Sign out]│
├──────────────────────────────────────────────────────────────────────┤
│ Settings                                                             │
│                                                                      │
│ Account                                                              │
│   Signed in as @octocat (id 583231). Token expires in 6h 12m and     │
│   refreshes automatically.                  [Sign out everywhere]    │
│                                                                      │
│ GitHub App installations                                             │
│   Webhooks only arrive for repositories where the app is installed.  │
│   ┌──────────────────────────────────────────────────────────────┐   │
│   │ acme (organization)   24 repositories   <Configure on GitHub> │   │
│   │ octocat (user)         3 repositories   <Configure on GitHub> │   │
│   └──────────────────────────────────────────────────────────────┘   │
│   [Install on another account]                                       │
│                                                                      │
│ Review data stored by burp                                           │
│   142 file review marks · 3 draft comments   [Delete all my data]    │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 5. Live update model (applies to every page)

```
 browser ──GET /inbox/stream (SSE, stays open)──▶ burp
 browser ──GET /pr/o/r/1/stream (SSE, stays open)──▶ burp
                                                    ▲
 GitHub ──POST /webhooks/github (HMAC-SHA256)──▶ burp ── bus.Publish(topic)
                                                    │
        every subscriber for that topic re-renders its fragment(s)
        and the SSE stream sends `datastar-patch-elements` events
```

Topics: `pr:{owner}/{repo}/{number}` for the review page and
`user:{github_login}` for the inbox (published when the user is the PR
author, a requested reviewer, or has reviewed/commented on the PR).

Because the source of truth is GitHub, a webhook only tells burp *that*
something changed; burp then re-fetches the affected data as the connected
user and pushes freshly rendered HTML. Users never see data their own GitHub
permissions would not allow.

---

## 6. Error and empty states

* Inbox with no results: each section renders "Nothing here 🎉" rather than
  disappearing, so the layout is stable.
* GitHub API failure while rendering a fragment: the fragment is replaced by
  an inline error box with a `[Retry]` button; the rest of the page stays
  interactive.
* Expired refresh token (six months without use): the user is redirected to
  `/` with "Your GitHub authorisation expired, please sign in again".
* SSE disconnect: datastar reconnects automatically (SSE retry); the stream
  handler sends the current state on connect, so nothing is lost.
