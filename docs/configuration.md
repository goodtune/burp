# Configuration

burp reads every setting from the environment. There is no configuration
file. Boolean values accept `true`/`false`, `1`/`0`; durations use Go syntax
(`30s`, `12h`, `720h`).

## Server

| Variable | Default | Description |
|---|---|---|
| `BURP_LISTEN` | `:8080` | Address to bind. |
| `BURP_BASE_URL` | `http://localhost:8080` | Public URL users reach burp on. Must be `https://` unless `BURP_DEV_MODE=true`. Used for the OAuth callback (`<base>/auth/callback`), the `Secure` cookie flag and the same-origin check. |
| `BURP_DEV_MODE` | `false` | Relax checks for local development: plain-HTTP base URL, no encryption key (sqlite/postgres store credentials in plaintext), no webhook secret (signatures not verified). |
| `BURP_SESSION_TTL` | `720h` | Browser session lifetime. |
| `BURP_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown bound. |
| `BURP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. `debug` adds an access log. |
| `BURP_LOG_FORMAT` | `text` | `text` or `json`. |

## TLS with ACME

burp serves plain HTTP by default and expects a proxy to terminate TLS. Set
`BURP_ACME_DIRECTORY` and burp serves HTTPS on `BURP_LISTEN` itself, using a
certificate it obtains from that ACME directory. It renews the certificate
in the background once a third of its lifetime remains, which suits
short-lived certificates from an internal CA. Any ACME CA works. For a
HashiCorp Vault PKI mount the directory is per role:
`https://vault.example.com:8200/v1/<mount>/roles/<role>/acme/directory`.

| Variable | Default | Description |
|---|---|---|
| `BURP_ACME_DIRECTORY` | | ACME directory URL. Setting it turns ACME on. Requires an `https://` `BURP_BASE_URL`. |
| `BURP_ACME_DOMAINS` | host of `BURP_BASE_URL` | Comma-separated names on the certificate. burp refuses TLS handshakes for any other name. |
| `BURP_ACME_EMAIL` | | Optional account contact. |
| `BURP_ACME_CACHE_DIR` | `acme` | Directory for the account key and issued certificates (created with mode 0700). Keep it on persistent storage. |
| `BURP_ACME_CA_FILE` | | PEM bundle to trust when burp talks to the ACME server, in addition to the system roots. Use it when the CA's own endpoint has a certificate from a private root, for example in a container. |
| `BURP_ACME_TLS_ALPN_LISTEN` | | Extra address, normally `:443`, that answers `tls-alpn-01` challenges and redirects other visitors to `BURP_BASE_URL`. |
| `BURP_ACME_HTTP_LISTEN` | | Extra address, normally `:80`, that answers `http-01` challenges and redirects other requests to `BURP_BASE_URL`. |

The CA checks a challenge on port 443 (`tls-alpn-01`) or port 80
(`http-01`) of the domain, whatever port burp itself listens on. If
`BURP_LISTEN` is `:443`, no extra listener is needed. Otherwise set one of
the two challenge listeners on a port that reaches burp. burp binds them at
startup and stops with an error if the port is already in use.

Browsers must trust the CA's root. Certificates from a Vault PKI mount
chain to that mount's CA (`/v1/<mount>/ca/pem`).

## GitHub App

| Variable | Default | Description |
|---|---|---|
| `BURP_GITHUB_CLIENT_ID` | required | The App's client id (`Iv1.…`). |
| `BURP_GITHUB_CLIENT_SECRET` | required | The App's client secret. |
| `BURP_GITHUB_WEBHOOK_SECRET` | required unless dev mode | Secret used to validate `X-Hub-Signature-256` on deliveries to `/webhooks/github`. |
| `BURP_GITHUB_APP_SLUG` | | URL slug of the App, used to build the install link. Optional when a private key is given: burp then reads it from `GET /app`. |
| `BURP_GITHUB_APP_ID` | | Numeric App id. Required with `BURP_GITHUB_PRIVATE_KEY`. |
| `BURP_GITHUB_PRIVATE_KEY` | | PEM private key (optional). `\n` sequences are unescaped so it can be passed on one line. |
| `BURP_GITHUB_PRIVATE_KEY_FILE` | | Path to the PEM file, alternative to the inline key. |
| `BURP_GITHUB_URL` | `https://github.com` | Web origin; change for GitHub Enterprise Server. |
| `BURP_GITHUB_API_URL` | `https://api.github.com` | API origin. For GHES use `https://ghes.example.com/api/v3`; the GraphQL endpoint is derived. |

burp acts as the user for every review operation, so the private key is only
needed for App-level metadata. Everything else uses the OAuth client id and
secret.

## Webhooks from ghp

burp can take webhook deliveries from a [ghp](https://github.com/goodtune/ghp)
server instead of, or as well as, directly from GitHub at
`/webhooks/github`. ghp verifies each delivery's signature, stores it in NATS
JetStream and serves it over server-sent events at
`GET /api/webhooks/stream`. burp keeps one stream open, reconnects with
backoff and resumes after the last delivery it saw. Use it when GitHub can't
reach burp, for example on a private network.

| Variable | Default | Description |
|---|---|---|
| `BURP_GHP_URL` | | ghp management URL, e.g. `https://ghp.example.com`. Setting it turns the subscription on. |
| `BURP_GHP_TOKEN` | | A ghp **admin** session token (from `ghp auth login`). |
| `BURP_GHP_TOKEN_COMMAND` | | Alternative to `BURP_GHP_TOKEN`: a shell command that prints the token, e.g. `dotvault kv get --field user_token ghp`. It runs on every connection, so a renewed token is used without a restart and nothing is written to disk. |
| `BURP_GHP_FILTER` | `github.webhooks.>` | NATS subject filter for the deliveries to receive. See ghp's webhook docs for the subject layout. |
| `BURP_GHP_CA_FILE` | | PEM bundle to trust for the ghp server, in addition to the system roots. |

Set exactly one of `BURP_GHP_TOKEN` and `BURP_GHP_TOKEN_COMMAND`. burp
starts from new deliveries; it does not replay ghp's stored log, because
every page fetches its state from GitHub when it loads. A delivery reaches
burp whichever GitHub App ghp received it for. burp uses deliveries only as
a signal to re-fetch, as the viewing user, the pages they affect, and it
logs events it has no use for at debug level. If GitHub also delivers
directly to `/webhooks/github`, the duplicate re-renders are merged by the
usual debounce.

The stream needs an admin session because ghp has no narrower credential
for webhook subscriptions yet.

## Storage

| Variable | Default | Description |
|---|---|---|
| `BURP_STORE_DRIVER` | `sqlite` | `sqlite`, `postgres` or `vault`. |
| `BURP_DATABASE_DSN` | `burp.db` (sqlite) | sqlite file path, or a PostgreSQL connection string (`postgres://user:pass@host/db?sslmode=require`). Required for postgres. |
| `BURP_ENCRYPTION_KEY` | required for sqlite/postgres unless dev mode | 32-byte hex key (64 characters) for AES-256-GCM encryption of GitHub tokens at rest. Generate with `burp keygen`. Rotating the key invalidates stored credentials; users sign in again. |

With `sqlite` and `postgres`, migrations run automatically on `burp serve`
(or explicitly with `burp migrate`). Vault needs no migrations.

## Vault backend

Used when `BURP_STORE_DRIVER=vault`. See [vault.md](vault.md).

| Variable | Default | Description |
|---|---|---|
| `BURP_VAULT_ADDR` | `$VAULT_ADDR` | Vault address. |
| `BURP_VAULT_MOUNT` | `secret` | KV v2 mount. |
| `BURP_VAULT_PATH` | `burp` | Key prefix inside the mount. |
| `BURP_VAULT_AUTH_METHOD` | `kubernetes` | `kubernetes`, `approle` or `token`. |
| `BURP_VAULT_K8S_ROLE` | required for kubernetes | Vault role bound to burp's service account. |
| `BURP_VAULT_K8S_MOUNT` | `kubernetes` | Auth mount path. |
| `BURP_VAULT_K8S_TOKEN_PATH` | `/var/run/secrets/kubernetes.io/serviceaccount/token` | Projected service account token. Re-read on every login so rotated tokens are picked up. |
| `BURP_VAULT_ROLE_ID` / `BURP_VAULT_SECRET_ID` | required for approle | AppRole credentials. |
| `BURP_VAULT_TOKEN` | `$VAULT_TOKEN` | Static token (development only). |

## Endpoints

| Path | Purpose |
|---|---|
| `/` | Sign-in page (redirects to the inbox when signed in). |
| `/auth/login`, `/auth/callback`, `/auth/logout`, `/auth/logout-all` | OAuth flow and sign-out. |
| `/inbox`, `/inbox/stream` | Inbox page and its SSE stream. |
| `/pr/{owner}/{repo}/{number}` (+ `/stream`, `/view`, actions) | Review page. |
| `/settings` | Account, installations, stored data. |
| `/webhooks/github` | GitHub App webhook receiver. |
| `/healthz`, `/readyz` | Liveness; readiness pings the store. |
| `/static/…` | Stylesheet and the vendored datastar bundle. |

## Security notes

* Session cookies are `HttpOnly`, `SameSite=Lax` and `Secure` on https.
  Only a SHA-256 hash of the cookie value is stored.
* Every mutating request must carry the `Datastar-Request: true` header and
  an `Origin`/`Referer` matching `BURP_BASE_URL`.
* Access and refresh tokens are encrypted with AES-256-GCM before being
  written to sqlite/postgres. With Vault they are stored verbatim inside
  Vault, which encrypts at rest.
* Webhook payloads are verified with HMAC-SHA256 and only used as a signal
  to re-fetch data as the affected user; burp never renders webhook content.
