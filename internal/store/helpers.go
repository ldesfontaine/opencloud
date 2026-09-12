package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// scanner est satisfait par *sql.Row et *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func notFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return machine.ErrNotFound
	}
	return nil
}

// Le pilote ne typpe pas ses erreurs : le code SQLite est dans le message.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var target interface{ Code() int }
	if errors.As(err, &target) {
		return target.Code() == 2067 || target.Code() == 1555
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
