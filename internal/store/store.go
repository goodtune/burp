// Package store defines the persistence interface used by burp and the
// records it keeps. GitHub remains the source of truth for pull requests,
// reviews and comments; burp only stores what GitHub cannot hold for it:
// user credentials, browser sessions, per-file "reviewed" marks and draft
// review comments.
//
// Two families of backend implement Store: SQL (sqlite, postgres) in
// package sqlstore, where credentials are encrypted by the caller before
// they are written, and HashiCorp Vault KV v2 in package vaultstore, which
// encrypts at rest itself.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by lookups when the record does not exist.
var ErrNotFound = errors.New("not found")

// User is a GitHub user that has signed in to burp.
type User struct {
	// ID is the GitHub numeric user id.
	ID        int64
	Login     string
	AvatarURL string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Credential is a user-to-server token pair issued to the GitHub App for
// a user. AccessToken and RefreshToken are stored encrypted (SQL backends)
// or verbatim inside Vault.
type Credential struct {
	UserID           int64
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	UpdatedAt        time.Time
}

// Session is a browser session. Only the SHA-256 hash of the cookie value
// is stored.
type Session struct {
	TokenHash string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

// OAuthState is an in-flight OAuth authorisation, keyed by the random
// state parameter.
type OAuthState struct {
	State     string
	ReturnTo  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// FileMark records that a user reviewed a file of a pull request at a
// particular head commit. There is at most one mark per (user, PR, path);
// re-marking updates HeadSHA.
type FileMark struct {
	UserID   int64
	Owner    string
	Repo     string
	Number   int
	Path     string
	HeadSHA  string
	MarkedAt time.Time
}

// Draft is a review comment held by burp until the user submits a review.
type Draft struct {
	ID     string
	UserID int64
	Owner  string
	Repo   string
	Number int
	Path   string
	// Side is "LEFT" or "RIGHT". Line is the line on that side; StartLine
	// is zero for single-line comments.
	Side      string
	Line      int
	StartLine int
	// CommitSHA is the head commit the diff was rendered against.
	CommitSHA string
	Body      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PRKey identifies a pull request.
type PRKey struct {
	Owner  string
	Repo   string
	Number int
}

// Store is implemented by every backend.
type Store interface {
	// Users.
	UpsertUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id int64) (*User, error)

	// Credentials.
	PutCredential(ctx context.Context, c *Credential) error
	GetCredential(ctx context.Context, userID int64) (*Credential, error)
	DeleteCredential(ctx context.Context, userID int64) error

	// Sessions.
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID int64) error

	// OAuth state. ConsumeOAuthState deletes the state as it reads it and
	// returns ErrNotFound for unknown or expired states.
	PutOAuthState(ctx context.Context, s *OAuthState) error
	ConsumeOAuthState(ctx context.Context, state string) (*OAuthState, error)

	// File review marks.
	SetFileMark(ctx context.Context, m *FileMark) error
	ClearFileMark(ctx context.Context, userID int64, key PRKey, path string) error
	ListFileMarks(ctx context.Context, userID int64, key PRKey) ([]FileMark, error)

	// Draft comments.
	PutDraft(ctx context.Context, d *Draft) error
	GetDraft(ctx context.Context, userID int64, id string) (*Draft, error)
	DeleteDraft(ctx context.Context, userID int64, id string) error
	ListDrafts(ctx context.Context, userID int64, key PRKey) ([]Draft, error)
	DeleteDrafts(ctx context.Context, userID int64, key PRKey) error

	// Stats and bulk deletion for the settings page.
	CountUserData(ctx context.Context, userID int64) (marks, drafts int, err error)
	DeleteUserData(ctx context.Context, userID int64) error

	// Cleanup removes expired sessions and OAuth states.
	Cleanup(ctx context.Context, now time.Time) error

	Ping(ctx context.Context) error
	Close() error
}
