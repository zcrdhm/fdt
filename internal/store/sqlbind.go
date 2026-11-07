package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

func normalizeDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return "sqlite"
	case "postgres", "pgx", "postgresql":
		return "postgres"
	default:
		return driver
	}
}

// bind rewrites SQLite '?' placeholders to Postgres $1..$n. Queries have no
// quoted question marks.
func (s *Store) bind(query string) string {
	if s == nil || s.driver != "postgres" {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 1
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.bind(query), args...)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.bind(query), args...)
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.bind(query), args...)
}
