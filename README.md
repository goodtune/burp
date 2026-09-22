# burp

burp is a web-based code review application for GitHub pull requests. It is a
single static Go binary (or container) that you host yourself, configured
entirely through environment variables, and it talks to GitHub **as the
signed-in user** through a GitHub App's user-to-server tokens. Reviews,
comments, approvals and merges you make in burp appear on GitHub under your
own account.

The frontend is server-rendered HTML driven by [datastar](https://data-star.dev):
the browser opens Server-Sent Event streams and the server pushes HTML
fragments whenever a GitHub webhook says something changed. There is no
client-side application framework.

## What it does

* **Inbox** ordered by what needs *you* next: needs your review, returned
  to you, approved and ready to merge, waiting on reviewers, drafts, recently
  merged. Filter with GitHub search qualifiers. Updates live on webhooks.
* **Review page** with an *action card* naming the one thing blocking merge,
  checks, reviews and thread counts, a revision picker for interdiffs
  (any commit range, or "since your last review"), a file navigator with
  per-file **reviewed** marks that know when a file changed after you marked
  it, unified or split diffs with inline threads, **draft comments batched
  into a single GitHub review**, replies, resolve/unresolve, merge, and
  keyboard shortcuts (`?`).
* **Live updates**: a `pull_request`, review, comment, thread, check or
  status webhook re-renders the affected page regions. New commits show a
  banner instead of changing the diff under you.
* **Credentials encrypted at rest** (AES-256-GCM, key from the environment)
  with sqlite or PostgreSQL, or stored in **HashiCorp Vault** (KV v2) using
  Kubernetes, AppRole or token auth.

See [docs/research.md](docs/research.md) for the Graphite / Reviewable
feature research this is based on and [docs/wireframes.md](docs/wireframes.md)
for the wireframes that define the UX.

## Quick start

1. Create a GitHub App ([docs/github-app.md](docs/github-app.md)).
2. Run burp:

```bash
export BURP_BASE_URL=https://burp.example.com
export BURP_GITHUB_CLIENT_ID=Iv1.xxxx
export BURP_GITHUB_CLIENT_SECRET=xxxx
export BURP_GITHUB_WEBHOOK_SECRET=xxxx
export BURP_GITHUB_APP_SLUG=my-burp
export BURP_ENCRYPTION_KEY=$(burp keygen)   # keep this safe
export BURP_DATABASE_DSN=/var/lib/burp/burp.db
burp serve
```

or with the container image (`make docker` builds `burp:<version>`):

```bash
docker run -p 8080:8080 --env-file burp.env -v burp-data:/var/lib/burp burp:dev serve
```

3. Open the base URL and sign in with GitHub. Install the App on the
   repositories you review so that webhooks reach burp.

The full list of settings is in [docs/configuration.md](docs/configuration.md);
the Vault backend is described in [docs/vault.md](docs/vault.md).

## Development

```bash
make check          # gofmt, go vet, go test
make build          # static binary ./burp
make docker         # container image
```

`BURP_DEV_MODE=true` allows an `http://` base URL, plain cookies, a missing
encryption key (credentials stored unencrypted, sqlite only) and a missing
webhook secret. Never use it in production.

The unit tests exercise every handler against a scripted GitHub, and the
store contract suite runs on sqlite always, and on PostgreSQL and Vault when
`BURP_TEST_POSTGRES_DSN` or `BURP_TEST_VAULT_ADDR`/`BURP_TEST_VAULT_TOKEN`
are set (CI does both). `e2e/` holds a Playwright suite that drives the real
UI in Chromium against `e2e/fakegh`, a scripted GitHub:

```bash
go build -o burp ./cmd/burp && go build -o e2e/fakegh/fakegh ./e2e/fakegh
cd e2e && npm ci && npx playwright install --with-deps chromium && npx playwright test
```

## Layout

```
cmd/burp/           entrypoint: serve, migrate, keygen, version
internal/config/    BURP_* environment configuration
internal/crypto/    AES-256-GCM encryption of credentials
internal/store/     Store interface, sqlite/postgres (sqlstore), Vault (vaultstore)
internal/gh/        GitHub App OAuth, token refresh, REST + GraphQL client, webhooks
internal/bus/       in-process pub/sub feeding SSE streams
internal/diff/      unified patch parser, unified/split layout
internal/web/       handlers, templates, datastar bundle, stylesheet
docs/               research, wireframes, configuration, GitHub App, Vault
```

## Limitations

* Live fan-out is in-process. Run one replica, or put webhook deliveries and
  page streams behind the same instance; multi-replica fan-out would need a
  shared bus (PostgreSQL LISTEN/NOTIFY or NATS) and is not implemented.
* Repositories without the App installed still work, but only refresh once
  a minute (inbox) or when you act (review page).
* GitHub keeps only the current commits of a branch. After a force push,
  "since your last review" still works while GitHub retains the old
  objects, which it normally does.
* Threads on a line are anchored by GitHub's `line`/`diffSide`; comments on
  outdated lines are listed at the top of the file.
* Draft comments are validated by GitHub at submission time. If the line a
  draft points at is no longer part of the diff, GitHub rejects the whole
  review and burp shows the error; delete or move the stale draft and submit
  again.
