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

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableMillis(d *time.Duration) any {
	if d == nil {
		return nil
	}
	return d.Milliseconds()
}

func timeOf(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0)
}

func intPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	i := int(value.Int64)
	return &i
}

func durationPointer(millis sql.NullInt64) *time.Duration {
	if !millis.Valid {
		return nil
	}
	d := time.Duration(millis.Int64) * time.Millisecond
	return &d
}
