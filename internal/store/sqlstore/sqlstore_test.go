package sqlstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/goodtune/burp/internal/store"
	"github.com/goodtune/burp/internal/store/storetest"
)

func TestSQLiteContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		return openSQLite(t)
	})
}

func openSQLite(t *testing.T) *Store {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "burp.db")
	s, err := Open(context.Background(), SQLite, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Migrate must be idempotent.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestPostgresContract runs when BURP_TEST_POSTGRES_DSN points at a scratch
// database; it is skipped otherwise.
func TestPostgresContract(t *testing.T) {
	dsn := os.Getenv("BURP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BURP_TEST_POSTGRES_DSN not set")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := Open(context.Background(), Postgres, dsn)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for _, tbl := range []string{"drafts", "file_marks", "oauth_states", "sessions", "credentials", "users", "schema_migrations"} {
			if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

func TestRebind(t *testing.T) {
	s := &Store{dialect: Postgres}
	got := s.q(`SELECT ? , ? FROM t WHERE a = ?`)
	if got != `SELECT $1 , $2 FROM t WHERE a = $3` {
		t.Fatal(got)
	}
	s2 := &Store{dialect: SQLite}
	if s2.q(`? ?`) != `? ?` {
		t.Fatal("sqlite must keep ? placeholders")
	}
}

func TestSQLiteDSN(t *testing.T) {
	if got := sqliteDSN("burp.db"); got != "file:burp.db?_pragma=foreign_keys%281%29&_pragma=journal_mode%28WAL%29&_pragma=busy_timeout%285000%29" {
		t.Fatal(got)
	}
	if got := sqliteDSN("file:x.db?mode=memory"); got[:len("file:x.db?mode=memory&")] != "file:x.db?mode=memory&" {
		t.Fatal(got)
	}
	if got := sqliteDSN("file:x.db?_pragma=foreign_keys(1)"); got != "file:x.db?_pragma=foreign_keys(1)" {
		t.Fatal(got)
	}
}
