// Package vaultstore implements store.Store on HashiCorp Vault's KV v2
// secrets engine. Vault encrypts at rest, so credentials are written
// verbatim and burp uses a passthrough Encryptor with this backend.
//
// Authentication is by the pod's projected Kubernetes service account
// token (recommended), AppRole, or a static token for development. Every
// KV operation is wrapped in withRelogin so that an expired Vault token is
// transparently renewed, following the pattern established in ghp.
//
// Layout under <mount>/data/<path>/:
//
//	users/<id>
//	credentials/<user id>
//	sessions/<token hash>
//	oauth/<state>
//	marks/<user id>/<owner>/<repo>/<number>/<sha256(path)>
//	drafts/<user id>/<owner>/<repo>/<number>/<draft id>
//
// Vault KV has no atomic read-modify-write for counters or queries, so
// list-style operations walk key prefixes. Expired sessions and states are
// removed by Cleanup, which scans the sessions/ and oauth/ prefixes.
package vaultstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"

	"github.com/goodtune/burp/internal/store"
)

// Auth methods.
const (
	AuthKubernetes = "kubernetes"
	AuthAppRole    = "approle"
	AuthToken      = "token"
)

// Config configures the Vault connection.
type Config struct {
	Addr  string
	Mount string // KV v2 mount, default "secret"
	Path  string // key prefix, default "burp"

	AuthMethod   string
	K8sRole      string
	K8sMount     string
	K8sTokenPath string
	RoleID       string
	SecretID     string
	Token        string
}

// KV is the minimal KV v2 surface the store needs. It is an interface so
// the store logic can be tested without a Vault server.
type KV interface {
	// Read returns the secret data at key or nil, nil if it does not exist.
	Read(ctx context.Context, key string) (map[string]any, error)
	Write(ctx context.Context, key string, data map[string]any) error
	// Delete removes all versions of the secret (metadata delete).
	Delete(ctx context.Context, key string) error
	// List returns child names under key; directories end with "/".
	List(ctx context.Context, key string) ([]string, error)
}

// Store is the Vault implementation.
type Store struct {
	kv KV
}

// New creates a Store from an authenticated Vault client.
func New(ctx context.Context, cfg Config) (*Store, error) {
	vc := vault.DefaultConfig()
	vc.Address = cfg.Addr
	client, err := vault.NewClient(vc)
	if err != nil {
		return nil, fmt.Errorf("creating vault client: %w", err)
	}
	kv := &vaultKV{client: client, cfg: cfg}
	if kv.cfg.Mount == "" {
		kv.cfg.Mount = "secret"
	}
	if kv.cfg.Path == "" {
		kv.cfg.Path = "burp"
	}
	if kv.cfg.AuthMethod == "" {
		kv.cfg.AuthMethod = AuthKubernetes
	}
	if kv.cfg.K8sMount == "" {
		kv.cfg.K8sMount = "kubernetes"
	}
	if kv.cfg.K8sTokenPath == "" {
		kv.cfg.K8sTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	if err := kv.login(ctx); err != nil {
		return nil, err
	}
	return &Store{kv: kv}, nil
}

// NewWithKV builds a Store on an arbitrary KV, used by tests.
func NewWithKV(kv KV) *Store { return &Store{kv: kv} }

// vaultKV adapts the Vault API client to KV.
type vaultKV struct {
	client *vault.Client
	cfg    Config
}

func (k *vaultKV) login(ctx context.Context) error {
	switch k.cfg.AuthMethod {
	case AuthToken:
		if k.cfg.Token == "" {
			return errors.New("vault token auth: token is required")
		}
		k.client.SetToken(k.cfg.Token)
		return nil
	case AuthAppRole:
		resp, err := k.client.Logical().WriteWithContext(ctx, "auth/approle/login", map[string]any{
			"role_id": k.cfg.RoleID, "secret_id": k.cfg.SecretID,
		})
		if err != nil {
			return fmt.Errorf("vault approle login: %w", err)
		}
		if resp == nil || resp.Auth == nil {
			return errors.New("vault approle login returned no token")
		}
		k.client.SetToken(resp.Auth.ClientToken)
		return nil
	case AuthKubernetes:
		jwt, err := os.ReadFile(k.cfg.K8sTokenPath)
		if err != nil {
			return fmt.Errorf("reading service account token: %w", err)
		}
		tok := strings.TrimSpace(string(jwt))
		if tok == "" {
			return fmt.Errorf("service account token at %s is empty", k.cfg.K8sTokenPath)
		}
		resp, err := k.client.Logical().WriteWithContext(ctx, "auth/"+strings.Trim(k.cfg.K8sMount, "/")+"/login", map[string]any{
			"role": k.cfg.K8sRole, "jwt": tok,
		})
		if err != nil {
			return fmt.Errorf("vault kubernetes login: %w", err)
		}
		if resp == nil || resp.Auth == nil {
			return errors.New("vault kubernetes login returned no token")
		}
		k.client.SetToken(resp.Auth.ClientToken)
		return nil
	default:
		return fmt.Errorf("unsupported vault auth method %q", k.cfg.AuthMethod)
	}
}

// withRelogin runs fn and, on a 403 (expired token), logs in again once
// and retries. The kubernetes JWT is re-read on every login so rotated
// projected tokens are picked up.
func (k *vaultKV) withRelogin(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	var respErr *vault.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == 403 {
		if lerr := k.login(ctx); lerr != nil {
			return fmt.Errorf("vault re-authentication failed: %w (original error: %v)", lerr, err)
		}
		return fn()
	}
	return err
}

func (k *vaultKV) dataPath(key string) string {
	return k.cfg.Mount + "/data/" + k.cfg.Path + "/" + key
}

func (k *vaultKV) metadataPath(key string) string {
	return k.cfg.Mount + "/metadata/" + k.cfg.Path + "/" + key
}

func (k *vaultKV) Read(ctx context.Context, key string) (map[string]any, error) {
	var secret *vault.Secret
	err := k.withRelogin(ctx, func() error {
		var rerr error
		secret, rerr = k.client.Logical().ReadWithContext(ctx, k.dataPath(key))
		return rerr
	})
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	data, ok := secret.Data["data"].(map[string]any)
	if !ok || data == nil {
		// Soft-deleted version.
		return nil, nil
	}
	return data, nil
}

func (k *vaultKV) Write(ctx context.Context, key string, data map[string]any) error {
	return k.withRelogin(ctx, func() error {
		_, err := k.client.Logical().WriteWithContext(ctx, k.dataPath(key), map[string]any{"data": data})
		return err
	})
}

func (k *vaultKV) Delete(ctx context.Context, key string) error {
	return k.withRelogin(ctx, func() error {
		_, err := k.client.Logical().DeleteWithContext(ctx, k.metadataPath(key))
		return err
	})
}

func (k *vaultKV) List(ctx context.Context, key string) ([]string, error) {
	var secret *vault.Secret
	err := k.withRelogin(ctx, func() error {
		var lerr error
		secret, lerr = k.client.Logical().ListWithContext(ctx, k.metadataPath(key))
		return lerr
	})
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	raw, _ := secret.Data["keys"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// Encoding helpers. All values are stored as strings to avoid float64
// round-trips through JSON.

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func i64(m map[string]any, key string) int64 {
	n, _ := strconv.ParseInt(str(m, key), 10, 64)
	return n
}

func tm(m map[string]any, key string) time.Time {
	s := str(m, key)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func pathHash(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:16])
}

func prPrefix(kind string, userID int64, key store.PRKey) string {
	return fmt.Sprintf("%s/%d/%s/%s/%d/", kind, userID, key.Owner, key.Repo, key.Number)
}

// walk lists every leaf key under prefix recursively.
func (s *Store) walk(ctx context.Context, prefix string) ([]string, error) {
	names, err := s.kv.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			sub, err := s.walk(ctx, prefix+n)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		out = append(out, prefix+n)
	}
	return out, nil
}

func (s *Store) Ping(ctx context.Context) error {
	_, err := s.kv.List(ctx, "users/")
	return err
}

func (s *Store) Close() error { return nil }

// Users.

func (s *Store) UpsertUser(ctx context.Context, u *store.User) error {
	key := "users/" + strconv.FormatInt(u.ID, 10)
	now := time.Now()
	existing, err := s.kv.Read(ctx, key)
	if err != nil {
		return fmt.Errorf("reading user: %w", err)
	}
	if existing != nil {
		u.CreatedAt = tm(existing, "created_at")
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	return s.kv.Write(ctx, key, map[string]any{
		"id": strconv.FormatInt(u.ID, 10), "login": u.Login, "avatar_url": u.AvatarURL,
		"created_at": fmtTime(u.CreatedAt), "updated_at": fmtTime(u.UpdatedAt),
	})
}

func (s *Store) GetUser(ctx context.Context, id int64) (*store.User, error) {
	m, err := s.kv.Read(ctx, "users/"+strconv.FormatInt(id, 10))
	if err != nil {
		return nil, fmt.Errorf("reading user: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("user %d: %w", id, store.ErrNotFound)
	}
	return &store.User{ID: i64(m, "id"), Login: str(m, "login"), AvatarURL: str(m, "avatar_url"), CreatedAt: tm(m, "created_at"), UpdatedAt: tm(m, "updated_at")}, nil
}

// Credentials.

func (s *Store) PutCredential(ctx context.Context, c *store.Credential) error {
	c.UpdatedAt = time.Now()
	return s.kv.Write(ctx, "credentials/"+strconv.FormatInt(c.UserID, 10), map[string]any{
		"user_id": strconv.FormatInt(c.UserID, 10), "access_token": c.AccessToken, "refresh_token": c.RefreshToken,
		"access_expires_at": fmtTime(c.AccessExpiresAt), "refresh_expires_at": fmtTime(c.RefreshExpiresAt), "updated_at": fmtTime(c.UpdatedAt),
	})
}

func (s *Store) GetCredential(ctx context.Context, userID int64) (*store.Credential, error) {
	m, err := s.kv.Read(ctx, "credentials/"+strconv.FormatInt(userID, 10))
	if err != nil {
		return nil, fmt.Errorf("reading credential: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("credential for user %d: %w", userID, store.ErrNotFound)
	}
	return &store.Credential{UserID: i64(m, "user_id"), AccessToken: str(m, "access_token"), RefreshToken: str(m, "refresh_token"),
		AccessExpiresAt: tm(m, "access_expires_at"), RefreshExpiresAt: tm(m, "refresh_expires_at"), UpdatedAt: tm(m, "updated_at")}, nil
}

func (s *Store) DeleteCredential(ctx context.Context, userID int64) error {
	return s.kv.Delete(ctx, "credentials/"+strconv.FormatInt(userID, 10))
}

// Sessions.

func (s *Store) CreateSession(ctx context.Context, sess *store.Session) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	return s.kv.Write(ctx, "sessions/"+sess.TokenHash, map[string]any{
		"token_hash": sess.TokenHash, "user_id": strconv.FormatInt(sess.UserID, 10),
		"created_at": fmtTime(sess.CreatedAt), "expires_at": fmtTime(sess.ExpiresAt),
	})
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*store.Session, error) {
	m, err := s.kv.Read(ctx, "sessions/"+tokenHash)
	if err != nil {
		return nil, fmt.Errorf("reading session: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("session: %w", store.ErrNotFound)
	}
	return &store.Session{TokenHash: str(m, "token_hash"), UserID: i64(m, "user_id"), CreatedAt: tm(m, "created_at"), ExpiresAt: tm(m, "expires_at")}, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	return s.kv.Delete(ctx, "sessions/"+tokenHash)
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	keys, err := s.walk(ctx, "sessions/")
	if err != nil {
		return err
	}
	want := strconv.FormatInt(userID, 10)
	for _, k := range keys {
		m, err := s.kv.Read(ctx, k)
		if err != nil {
			return err
		}
		if m != nil && str(m, "user_id") == want {
			if err := s.kv.Delete(ctx, k); err != nil {
				return err
			}
		}
	}
	return nil
}

// OAuth state.

func (s *Store) PutOAuthState(ctx context.Context, st *store.OAuthState) error {
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now()
	}
	return s.kv.Write(ctx, "oauth/"+st.State, map[string]any{
		"state": st.State, "return_to": st.ReturnTo, "created_at": fmtTime(st.CreatedAt), "expires_at": fmtTime(st.ExpiresAt),
	})
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*store.OAuthState, error) {
	key := "oauth/" + state
	m, err := s.kv.Read(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("reading oauth state: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("oauth state: %w", store.ErrNotFound)
	}
	if err := s.kv.Delete(ctx, key); err != nil {
		return nil, fmt.Errorf("deleting oauth state: %w", err)
	}
	st := &store.OAuthState{State: str(m, "state"), ReturnTo: str(m, "return_to"), CreatedAt: tm(m, "created_at"), ExpiresAt: tm(m, "expires_at")}
	if !st.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("oauth state expired: %w", store.ErrNotFound)
	}
	return st, nil
}

// File marks.

func (s *Store) SetFileMark(ctx context.Context, m *store.FileMark) error {
	m.MarkedAt = time.Now()
	key := prPrefix("marks", m.UserID, store.PRKey{Owner: m.Owner, Repo: m.Repo, Number: m.Number}) + pathHash(m.Path)
	return s.kv.Write(ctx, key, map[string]any{
		"user_id": strconv.FormatInt(m.UserID, 10), "owner": m.Owner, "repo": m.Repo, "number": strconv.Itoa(m.Number),
		"path": m.Path, "head_sha": m.HeadSHA, "marked_at": fmtTime(m.MarkedAt),
	})
}

func (s *Store) ClearFileMark(ctx context.Context, userID int64, key store.PRKey, path string) error {
	return s.kv.Delete(ctx, prPrefix("marks", userID, key)+pathHash(path))
}

func (s *Store) ListFileMarks(ctx context.Context, userID int64, key store.PRKey) ([]store.FileMark, error) {
	prefix := prPrefix("marks", userID, key)
	names, err := s.kv.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing marks: %w", err)
	}
	out := make([]store.FileMark, 0, len(names))
	for _, n := range names {
		m, err := s.kv.Read(ctx, prefix+n)
		if err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		out = append(out, store.FileMark{UserID: i64(m, "user_id"), Owner: str(m, "owner"), Repo: str(m, "repo"), Number: int(i64(m, "number")),
			Path: str(m, "path"), HeadSHA: str(m, "head_sha"), MarkedAt: tm(m, "marked_at")})
	}
	return out, nil
}

// Drafts.

func (s *Store) PutDraft(ctx context.Context, d *store.Draft) error {
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	key := prPrefix("drafts", d.UserID, store.PRKey{Owner: d.Owner, Repo: d.Repo, Number: d.Number}) + d.ID
	return s.kv.Write(ctx, key, map[string]any{
		"id": d.ID, "user_id": strconv.FormatInt(d.UserID, 10), "owner": d.Owner, "repo": d.Repo, "number": strconv.Itoa(d.Number),
		"path": d.Path, "side": d.Side, "line": strconv.Itoa(d.Line), "start_line": strconv.Itoa(d.StartLine), "commit_sha": d.CommitSHA,
		"body": d.Body, "created_at": fmtTime(d.CreatedAt), "updated_at": fmtTime(d.UpdatedAt),
	})
}

func decodeDraft(m map[string]any) store.Draft {
	return store.Draft{ID: str(m, "id"), UserID: i64(m, "user_id"), Owner: str(m, "owner"), Repo: str(m, "repo"), Number: int(i64(m, "number")),
		Path: str(m, "path"), Side: str(m, "side"), Line: int(i64(m, "line")), StartLine: int(i64(m, "start_line")), CommitSHA: str(m, "commit_sha"),
		Body: str(m, "body"), CreatedAt: tm(m, "created_at"), UpdatedAt: tm(m, "updated_at")}
}

// findDraft locates a draft by id under the user's drafts prefix. Drafts
// are keyed by PR so a lookup by id alone walks the user's subtree.
func (s *Store) findDraft(ctx context.Context, userID int64, id string) (string, map[string]any, error) {
	keys, err := s.walk(ctx, "drafts/"+strconv.FormatInt(userID, 10)+"/")
	if err != nil {
		return "", nil, err
	}
	for _, k := range keys {
		if strings.HasSuffix(k, "/"+id) {
			m, err := s.kv.Read(ctx, k)
			if err != nil {
				return "", nil, err
			}
			if m != nil {
				return k, m, nil
			}
		}
	}
	return "", nil, nil
}

func (s *Store) GetDraft(ctx context.Context, userID int64, id string) (*store.Draft, error) {
	_, m, err := s.findDraft(ctx, userID, id)
	if err != nil {
		return nil, fmt.Errorf("finding draft: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("draft %s: %w", id, store.ErrNotFound)
	}
	d := decodeDraft(m)
	return &d, nil
}

func (s *Store) DeleteDraft(ctx context.Context, userID int64, id string) error {
	key, m, err := s.findDraft(ctx, userID, id)
	if err != nil {
		return fmt.Errorf("finding draft: %w", err)
	}
	if m == nil {
		return nil
	}
	return s.kv.Delete(ctx, key)
}

func (s *Store) ListDrafts(ctx context.Context, userID int64, key store.PRKey) ([]store.Draft, error) {
	prefix := prPrefix("drafts", userID, key)
	names, err := s.kv.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing drafts: %w", err)
	}
	out := make([]store.Draft, 0, len(names))
	for _, n := range names {
		m, err := s.kv.Read(ctx, prefix+n)
		if err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		out = append(out, decodeDraft(m))
	}
	sortDrafts(out)
	return out, nil
}

func sortDrafts(ds []store.Draft) {
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && draftLess(ds[j], ds[j-1]); j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
}

func draftLess(a, b store.Draft) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

func (s *Store) DeleteDrafts(ctx context.Context, userID int64, key store.PRKey) error {
	keys, err := s.walk(ctx, prPrefix("drafts", userID, key))
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.kv.Delete(ctx, k); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CountUserData(ctx context.Context, userID int64) (int, int, error) {
	uid := strconv.FormatInt(userID, 10)
	marks, err := s.walk(ctx, "marks/"+uid+"/")
	if err != nil {
		return 0, 0, err
	}
	drafts, err := s.walk(ctx, "drafts/"+uid+"/")
	if err != nil {
		return 0, 0, err
	}
	return len(marks), len(drafts), nil
}

func (s *Store) DeleteUserData(ctx context.Context, userID int64) error {
	uid := strconv.FormatInt(userID, 10)
	for _, prefix := range []string{"marks/" + uid + "/", "drafts/" + uid + "/"} {
		keys, err := s.walk(ctx, prefix)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := s.kv.Delete(ctx, k); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Cleanup(ctx context.Context, now time.Time) error {
	for _, prefix := range []string{"sessions/", "oauth/"} {
		keys, err := s.walk(ctx, prefix)
		if err != nil {
			return err
		}
		for _, k := range keys {
			m, err := s.kv.Read(ctx, k)
			if err != nil {
				return err
			}
			if m == nil {
				continue
			}
			if exp := tm(m, "expires_at"); !exp.After(now) {
				if err := s.kv.Delete(ctx, k); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
