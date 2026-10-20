package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// scanner est satisfait par *sql.Row et *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// querier est satisfait par *sql.DB et *sql.Tx : de quoi lire.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// execer est satisfait par *sql.DB et *sql.Tx : de quoi écrire.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// inTx ouvre une transaction, la confie à write et la valide ; what nomme
// l'opération dans les erreurs (« begin what », « commit what »). Si write
// échoue, la transaction est annulée.
func (db *DB) inTx(ctx context.Context, what string, write func(tx *sql.Tx) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", what, err)
	}
	defer tx.Rollback()

	if err := write(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", what, err)
	}
	return nil
}

// collect lit toutes les lignes de rows avec scan et ferme rows. Sans ligne,
// la liste est vide, jamais nil : une seule forme pour tout le store.
func collect[T any](rows *sql.Rows, scan func(scanner) (T, error)) ([]T, error) {
	defer rows.Close()
	list := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

// notFoundIfNoRow rend notFound quand aucune ligne n'a été touchée ; plusieurs
// lignes ne sont pas une erreur.
func notFoundIfNoRow(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
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
