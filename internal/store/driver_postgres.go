package store

import (
	"database/sql"

	"github.com/jackc/pgx/v5/stdlib"
)

func init() {
	sql.Register("postgres", stdlib.GetDefaultDriver())
}
