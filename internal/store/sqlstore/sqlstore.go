// Package sqlstore implements store.Store on database/sql for sqlite
// (modernc.org/sqlite, pure Go) and postgres (pgx). Timestamps are stored
// as unix milliseconds so that a single set of migrations serves both
// engines. Credentials must be encrypted by the caller before PutCredential.
package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver
	_ "modernc.org/sqlite"             // sqlite driver

	"github.com/goodtune/burp/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Dialects.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// Store is the SQL implementation.
type Store struct {
	db      *sql.DB
	dialect string
}

// Open connects to the database. dialect is SQLite or Postgres.
func Open(ctx context.Context, dialect, dsn string) (*Store, error) {
	var db *sql.DB
	var err error
	switch dialect {
	case SQLite:
		db, err = sql.Open("sqlite", sqliteDSN(dsn))
		if err == nil {
			// A single writer avoids SQLITE_BUSY storms; WAL lets readers proceed.
			db.SetMaxOpenConns(1)
		}
	case Postgres:
		db, err = sql.Open("pgx", dsn)
	default:
		return nil, fmt.Errorf("unsupported sql dialect %q", dialect)
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dialect, err)
	}
	s := &Store{db: db, dialect: dialect}
	if err := s.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// sqliteDSN adds the pragmas burp relies on unless the caller set them.
func sqliteDSN(dsn string) string {
	if strings.Contains(dsn, "_pragma") {
		return dsn
	}
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	return dsn + sep + q.Encode()
}

// Migrate applies embedded migrations that have not run yet.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`), name).Scan(&n); err != nil {
			return fmt.Errorf("checking migration %s: %w", name, err)
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`), name, ms(time.Now())); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// q rewrites ? placeholders to $n for postgres.
func (s *Store) q(query string) string {
	if s.dialect != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Close() error                   { return s.db.Close() }

// Users.

func (s *Store) UpsertUser(ctx context.Context, u *store.User) error {
	now := now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO users (id, login, avatar_url, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET login = excluded.login, avatar_url = excluded.avatar_url, updated_at = excluded.updated_at`),
		u.ID, u.Login, u.AvatarURL, ms(u.CreatedAt), ms(u.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upserting user: %w", err)
	}
	return nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (*store.User, error) {
	var u store.User
	var c, m int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id, login, avatar_url, created_at, updated_at FROM users WHERE id = ?`), id).
		Scan(&u.ID, &u.Login, &u.AvatarURL, &c, &m)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("user %d: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = fromMS(c), fromMS(m)
	return &u, nil
}

// Credentials.

func (s *Store) PutCredential(ctx context.Context, c *store.Credential) error {
	c.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO credentials (user_id, access_token, refresh_token, access_expires_at, refresh_expires_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET access_token = excluded.access_token, refresh_token = excluded.refresh_token,
		access_expires_at = excluded.access_expires_at, refresh_expires_at = excluded.refresh_expires_at, updated_at = excluded.updated_at`),
		c.UserID, c.AccessToken, c.RefreshToken, ms(c.AccessExpiresAt), ms(c.RefreshExpiresAt), ms(c.UpdatedAt))
	if err != nil {
		return fmt.Errorf("putting credential: %w", err)
	}
	return nil
}

func (s *Store) GetCredential(ctx context.Context, userID int64) (*store.Credential, error) {
	var c store.Credential
	var a, r, u int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT user_id, access_token, refresh_token, access_expires_at, refresh_expires_at, updated_at FROM credentials WHERE user_id = ?`), userID).
		Scan(&c.UserID, &c.AccessToken, &c.RefreshToken, &a, &r, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("credential for user %d: %w", userID, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting credential: %w", err)
	}
	c.AccessExpiresAt, c.RefreshExpiresAt, c.UpdatedAt = fromMS(a), fromMS(r), fromMS(u)
	return &c, nil
}

func (s *Store) DeleteCredential(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM credentials WHERE user_id = ?`), userID)
	if err != nil {
		return fmt.Errorf("deleting credential: %w", err)
	}
	return nil
}

// Sessions.

func (s *Store) CreateSession(ctx context.Context, sess *store.Session) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`),
		sess.TokenHash, sess.UserID, ms(sess.CreatedAt), ms(sess.ExpiresAt))
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, tokenHash string) (*store.Session, error) {
	var sess store.Session
	var c, e int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT token_hash, user_id, created_at, expires_at FROM sessions WHERE token_hash = ?`), tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &c, &e)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session: %w", store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	sess.CreatedAt, sess.ExpiresAt = fromMS(c), fromMS(e)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE token_hash = ?`), tokenHash)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id = ?`), userID)
	if err != nil {
		return fmt.Errorf("deleting user sessions: %w", err)
	}
	return nil
}

// OAuth state.

func (s *Store) PutOAuthState(ctx context.Context, st *store.OAuthState) error {
	if st.CreatedAt.IsZero() {
		st.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO oauth_states (state, return_to, created_at, expires_at) VALUES (?, ?, ?, ?)`),
		st.State, st.ReturnTo, ms(st.CreatedAt), ms(st.ExpiresAt))
	if err != nil {
		return fmt.Errorf("putting oauth state: %w", err)
	}
	return nil
}

func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*store.OAuthState, error) {
	var st store.OAuthState
	var c, e int64
	err := s.db.QueryRowContext(ctx, s.q(`DELETE FROM oauth_states WHERE state = ? RETURNING state, return_to, created_at, expires_at`), state).
		Scan(&st.State, &st.ReturnTo, &c, &e)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("oauth state: %w", store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("consuming oauth state: %w", err)
	}
	st.CreatedAt, st.ExpiresAt = fromMS(c), fromMS(e)
	if !st.ExpiresAt.After(now()) {
		return nil, fmt.Errorf("oauth state expired: %w", store.ErrNotFound)
	}
	return &st, nil
}

// File marks.

func (s *Store) SetFileMark(ctx context.Context, m *store.FileMark) error {
	m.MarkedAt = now()
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO file_marks (user_id, owner, repo, number, path, head_sha, marked_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, owner, repo, number, path) DO UPDATE SET head_sha = excluded.head_sha, marked_at = excluded.marked_at`),
		m.UserID, m.Owner, m.Repo, m.Number, m.Path, m.HeadSHA, ms(m.MarkedAt))
	if err != nil {
		return fmt.Errorf("setting file mark: %w", err)
	}
	return nil
}

func (s *Store) ClearFileMark(ctx context.Context, userID int64, key store.PRKey, path string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM file_marks WHERE user_id = ? AND owner = ? AND repo = ? AND number = ? AND path = ?`),
		userID, key.Owner, key.Repo, key.Number, path)
	if err != nil {
		return fmt.Errorf("clearing file mark: %w", err)
	}
	return nil
}

func (s *Store) ListFileMarks(ctx context.Context, userID int64, key store.PRKey) ([]store.FileMark, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT user_id, owner, repo, number, path, head_sha, marked_at FROM file_marks WHERE user_id = ? AND owner = ? AND repo = ? AND number = ? ORDER BY path`),
		userID, key.Owner, key.Repo, key.Number)
	if err != nil {
		return nil, fmt.Errorf("listing file marks: %w", err)
	}
	defer rows.Close()
	out := make([]store.FileMark, 0)
	for rows.Next() {
		var m store.FileMark
		var at int64
		if err := rows.Scan(&m.UserID, &m.Owner, &m.Repo, &m.Number, &m.Path, &m.HeadSHA, &at); err != nil {
			return nil, err
		}
		m.MarkedAt = fromMS(at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Drafts.

func (s *Store) PutDraft(ctx context.Context, d *store.Draft) error {
	now := now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO drafts (id, user_id, owner, repo, number, path, side, line, start_line, commit_sha, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET path = excluded.path, side = excluded.side, line = excluded.line, start_line = excluded.start_line,
		commit_sha = excluded.commit_sha, body = excluded.body, updated_at = excluded.updated_at`),
		d.ID, d.UserID, d.Owner, d.Repo, d.Number, d.Path, d.Side, d.Line, d.StartLine, d.CommitSHA, d.Body, ms(d.CreatedAt), ms(d.UpdatedAt))
	if err != nil {
		return fmt.Errorf("putting draft: %w", err)
	}
	return nil
}

const draftCols = `id, user_id, owner, repo, number, path, side, line, start_line, commit_sha, body, created_at, updated_at`

func scanDraft(sc interface{ Scan(...any) error }) (*store.Draft, error) {
	var d store.Draft
	var c, u int64
	if err := sc.Scan(&d.ID, &d.UserID, &d.Owner, &d.Repo, &d.Number, &d.Path, &d.Side, &d.Line, &d.StartLine, &d.CommitSHA, &d.Body, &c, &u); err != nil {
		return nil, err
	}
	d.CreatedAt, d.UpdatedAt = fromMS(c), fromMS(u)
	return &d, nil
}

func (s *Store) GetDraft(ctx context.Context, userID int64, id string) (*store.Draft, error) {
	d, err := scanDraft(s.db.QueryRowContext(ctx, s.q(`SELECT `+draftCols+` FROM drafts WHERE id = ? AND user_id = ?`), id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("draft %s: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("getting draft: %w", err)
	}
	return d, nil
}

func (s *Store) DeleteDraft(ctx context.Context, userID int64, id string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM drafts WHERE id = ? AND user_id = ?`), id, userID)
	if err != nil {
		return fmt.Errorf("deleting draft: %w", err)
	}
	return nil
}

func (s *Store) ListDrafts(ctx context.Context, userID int64, key store.PRKey) ([]store.Draft, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+draftCols+` FROM drafts WHERE user_id = ? AND owner = ? AND repo = ? AND number = ? ORDER BY path, line, created_at`),
		userID, key.Owner, key.Repo, key.Number)
	if err != nil {
		return nil, fmt.Errorf("listing drafts: %w", err)
	}
	defer rows.Close()
	out := make([]store.Draft, 0)
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDrafts(ctx context.Context, userID int64, key store.PRKey) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM drafts WHERE user_id = ? AND owner = ? AND repo = ? AND number = ?`),
		userID, key.Owner, key.Repo, key.Number)
	if err != nil {
		return fmt.Errorf("deleting drafts: %w", err)
	}
	return nil
}

func (s *Store) CountUserData(ctx context.Context, userID int64) (int, int, error) {
	var marks, drafts int
	if err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM file_marks WHERE user_id = ?`), userID).Scan(&marks); err != nil {
		return 0, 0, fmt.Errorf("counting marks: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM drafts WHERE user_id = ?`), userID).Scan(&drafts); err != nil {
		return 0, 0, fmt.Errorf("counting drafts: %w", err)
	}
	return marks, drafts, nil
}

func (s *Store) DeleteUserData(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM file_marks WHERE user_id = ?`), userID); err != nil {
		return fmt.Errorf("deleting marks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM drafts WHERE user_id = ?`), userID); err != nil {
		return fmt.Errorf("deleting drafts: %w", err)
	}
	return tx.Commit()
}

func (s *Store) Cleanup(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE expires_at <= ?`), ms(now)); err != nil {
		return fmt.Errorf("cleaning sessions: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, s.q(`DELETE FROM oauth_states WHERE expires_at <= ?`), ms(now)); err != nil {
		return fmt.Errorf("cleaning oauth states: %w", err)
	}
	return nil
}

// now returns the current time at the millisecond precision the schema
// stores, so values written and read back compare equal.
func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }
