# Vault storage backend

With `BURP_STORE_DRIVER=vault` burp keeps all its records (users,
credentials, sessions, OAuth state, review marks, drafts) in Vault's KV v2
secrets engine. Vault encrypts at rest, so `BURP_ENCRYPTION_KEY` is not
used. This follows the design of [ghp](https://github.com/goodtune/ghp).

Layout under `<mount>/data/<path>/`:

```
users/<github id>
credentials/<github id>
sessions/<sha256 of cookie>
oauth/<state>
marks/<github id>/<owner>/<repo>/<number>/<sha256 of file path>
drafts/<github id>/<owner>/<repo>/<number>/<draft id>
```

## Policy

```hcl
path "secret/data/burp/*"     { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/burp/*" { capabilities = ["read", "list", "delete"] }
```

Adjust `secret` and `burp` to `BURP_VAULT_MOUNT` and `BURP_VAULT_PATH`.

## Kubernetes auth (recommended)

```bash
vault auth enable kubernetes
vault write auth/kubernetes/config \
  kubernetes_host="https://kubernetes.default.svc:443"
vault write auth/kubernetes/role/burp \
  bound_service_account_names=burp \
  bound_service_account_namespaces=burp \
  policies=burp token_ttl=1h token_max_ttl=4h
```

Pod environment:

```yaml
env:
  - name: BURP_STORE_DRIVER
    value: vault
  - name: BURP_VAULT_ADDR
    value: https://vault.vault.svc:8200
  - name: BURP_VAULT_AUTH_METHOD
    value: kubernetes
  - name: BURP_VAULT_K8S_ROLE
    value: burp
```

burp reads the projected service account token on every login, so token
rotation by the kubelet is transparent. When Vault answers 403 (expired
token) burp logs in again once and retries the operation.

## AppRole and token auth

```bash
export BURP_VAULT_AUTH_METHOD=approle BURP_VAULT_ROLE_ID=… BURP_VAULT_SECRET_ID=…
# development only:
export BURP_VAULT_AUTH_METHOD=token BURP_VAULT_TOKEN=root
```

## Limitations

* Vault KV has no queries: listing sessions to sign a user out everywhere,
  counting a user's data and the periodic cleanup walk key prefixes. This
  is fine for the sizes burp deals with.
* There is no atomic read-modify-write; burp only ever overwrites whole
  records so no counters are involved.
* The contract test suite runs against a live Vault when
  `BURP_TEST_VAULT_ADDR` and `BURP_TEST_VAULT_TOKEN` are set
  (`vault server -dev`), and always against an in-memory KV.
