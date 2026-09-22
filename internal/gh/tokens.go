package gh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/goodtune/burp/internal/crypto"
	"github.com/goodtune/burp/internal/store"
)

// ErrReauthRequired means burp holds no usable credential for the user and
// they must sign in with GitHub again.
var ErrReauthRequired = errors.New("github authorisation expired, sign in again")

// refreshLeeway is how long before expiry a token is refreshed.
const refreshLeeway = 5 * time.Minute

// Tokens hands out valid access tokens for users, refreshing and
// re-encrypting them as needed. Concurrent requests for the same user
// share one refresh.
type Tokens struct {
	Store  store.Store
	Enc    *crypto.Encryptor
	OAuth  *OAuth
	Logger *slog.Logger
	Now    func() time.Time

	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tokens) userLock(id int64) *sync.Mutex {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.locks == nil {
		t.locks = map[int64]*sync.Mutex{}
	}
	l, ok := t.locks[id]
	if !ok {
		l = &sync.Mutex{}
		t.locks[id] = l
	}
	return l
}

// Save encrypts and stores a freshly issued token pair.
func (t *Tokens) Save(ctx context.Context, userID int64, tr *TokenResponse) (*store.Credential, error) {
	encA, err := t.Enc.Encrypt(tr.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("encrypting access token: %w", err)
	}
	encR, err := t.Enc.Encrypt(tr.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("encrypting refresh token: %w", err)
	}
	now := t.now()
	c := &store.Credential{
		UserID:           userID,
		AccessToken:      encA,
		RefreshToken:     encR,
		AccessExpiresAt:  tr.AccessExpiry(now),
		RefreshExpiresAt: tr.RefreshExpiry(now),
	}
	if tr.RefreshToken == "" {
		// Non-expiring user token (App configured without token expiry).
		c.RefreshExpiresAt = time.Time{}
	}
	if err := t.Store.PutCredential(ctx, c); err != nil {
		return nil, fmt.Errorf("storing credential: %w", err)
	}
	return c, nil
}

// AccessToken returns a plaintext access token that is valid for at least
// refreshLeeway, refreshing it if necessary.
func (t *Tokens) AccessToken(ctx context.Context, userID int64) (string, error) {
	l := t.userLock(userID)
	l.Lock()
	defer l.Unlock()

	c, err := t.Store.GetCredential(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrReauthRequired
	}
	if err != nil {
		return "", err
	}
	now := t.now()
	if c.AccessExpiresAt.After(now.Add(refreshLeeway)) {
		return t.Enc.Decrypt(c.AccessToken)
	}
	refresh, err := t.Enc.Decrypt(c.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("decrypting refresh token: %w", err)
	}
	if refresh == "" {
		// Non-expiring token: keep using it.
		return t.Enc.Decrypt(c.AccessToken)
	}
	if !c.RefreshExpiresAt.IsZero() && !c.RefreshExpiresAt.After(now) {
		return "", ErrReauthRequired
	}
	tr, err := t.OAuth.Refresh(ctx, refresh)
	if err != nil {
		var oe *OAuthError
		if errors.As(err, &oe) && oe.Permanent() {
			if t.Logger != nil {
				t.Logger.Warn("github refresh token rejected", "user_id", userID, "error", err)
			}
			_ = t.Store.DeleteCredential(ctx, userID)
			return "", ErrReauthRequired
		}
		return "", fmt.Errorf("refreshing github token: %w", err)
	}
	if _, err := t.Save(ctx, userID, tr); err != nil {
		return "", err
	}
	if t.Logger != nil {
		t.Logger.Info("github token refreshed", "user_id", userID)
	}
	return tr.AccessToken, nil
}

// Expiry reports when the stored access token expires, for the settings
// page. It returns the zero time when no credential exists.
func (t *Tokens) Expiry(ctx context.Context, userID int64) (time.Time, error) {
	c, err := t.Store.GetCredential(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return c.AccessExpiresAt, nil
}
