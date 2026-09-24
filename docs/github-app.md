# Creating the GitHub App

burp is a GitHub App. Users authorise it (user-to-server tokens) and burp
then calls GitHub as them; repositories install it so that webhooks reach
burp.

## 1. Register the App

GitHub → Settings → Developer settings → GitHub Apps → New GitHub App.

| Field | Value |
|---|---|
| Homepage URL | `https://burp.example.com` |
| Callback URL | `https://burp.example.com/auth/callback` |
| Expire user authorization tokens | **enabled** (burp refreshes 8 hour tokens with the 6 month refresh token) |
| Request user authorization (OAuth) during installation | enabled (recommended) |
| Setup URL | `https://burp.example.com/auth/callback`, "Redirect on update" enabled |
| Webhook | active, URL `https://burp.example.com/webhooks/github`, secret = `BURP_GITHUB_WEBHOOK_SECRET` |

### Permissions

Repository permissions:

| Permission | Access | Why |
|---|---|---|
| Pull requests | Read and write | list, review, comment, merge, mirror "viewed" marks |
| Contents | Read and write | merge pull requests, compare commits |
| Checks | Read | check runs in the header |
| Commit statuses | Read | legacy statuses |
| Metadata | Read | mandatory |
| Issues | Read and write | conversation comments (PR comments are issue comments) |

Reviewed marks are mirrored to GitHub's per-file "Viewed" checkbox with
the `markFileAsViewed` mutation. If GitHub answers "Resource not accessible
by integration", the App is missing Pull requests write access, or the
installation has not accepted an updated permission set yet (owners are
asked to approve permission changes under the installation's settings).
burp keeps its own mark either way and says so on the page.

Account permissions: none. Because burp acts as the user, the effective
permissions are the intersection of the App's permissions and what the user
can do on GitHub.

### Subscribe to events

`Pull request`, `Pull request review`, `Pull request review comment`,
`Pull request review thread`, `Issue comment`, `Check run`, `Check suite`,
`Status`, `Push`.

## 2. Collect the credentials

* Client ID → `BURP_GITHUB_CLIENT_ID`
* Generate a client secret → `BURP_GITHUB_CLIENT_SECRET`
* Webhook secret → `BURP_GITHUB_WEBHOOK_SECRET`
* App slug (from the App's public page URL `github.com/apps/<slug>`) →
  `BURP_GITHUB_APP_SLUG`
* Optional: App ID and a generated private key → `BURP_GITHUB_APP_ID`,
  `BURP_GITHUB_PRIVATE_KEY` (lets burp resolve the slug itself and log the
  App identity at start-up)

## 3. Install the App

Install it on the organisations or repositories you review
(`https://github.com/apps/<slug>/installations/new`, also linked from the
sign-in and settings pages). Repositories without the App still work
through the user's token; they just do not push live updates.

## GitHub Enterprise Server

Set `BURP_GITHUB_URL=https://ghes.example.com` and
`BURP_GITHUB_API_URL=https://ghes.example.com/api/v3`.
