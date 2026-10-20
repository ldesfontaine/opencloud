package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type fakeResult struct {
	affected int64
	err      error
}

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.affected, r.err }

func TestNotFoundIfNoRow_NamesWhatIsMissingOnlyWhenNothingChanged(t *testing.T) {
	missing := errors.New("missing")
	if err := notFoundIfNoRow(fakeResult{affected: 1}, missing); err != nil {
		t.Fatalf("one row touched: got %v", err)
	}
	if err := notFoundIfNoRow(fakeResult{affected: 2}, missing); err != nil {
		t.Fatalf("several rows touched: got %v, want nil", err)
	}
	if err := notFoundIfNoRow(fakeResult{}, missing); !errors.Is(err, missing) {
		t.Fatalf("no row touched: got %v, want the missing error", err)
	}
}

func TestNotFoundIfNoRow_KeepsTheDriverErrorApart(t *testing.T) {
	missing := errors.New("missing")
	broken := errors.New("driver failure")
	err := notFoundIfNoRow(fakeResult{err: broken}, missing)
	if !errors.Is(err, broken) || errors.Is(err, missing) {
		t.Fatalf("got %v, want the driver error and not the missing one", err)
	}
}

func TestInTx_RollsBackWhenWriteFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	failure := errors.New("write failed")
	err := db.inTx(ctx, "test", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, name, title, created_at, updated_at) VALUES ('p', 'p', '', 0, 0)`); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("got %v, want the write error", err)
	}
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d project(s) written by a failed transaction", count)
	}
}

func TestCollect_RendersAnEmptyListNotNil(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.sql.QueryContext(context.Background(), `SELECT id FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := collect(rows, func(row scanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids == nil || len(ids) != 0 {
		t.Fatalf("got %#v, want an empty non-nil list", ids)
	}
}

func TestCollect_StopsOnAScanError(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO projects (id, name, title, created_at, updated_at) VALUES ('p', 'p', '', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.sql.QueryContext(ctx, `SELECT id FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	broken := errors.New("scan failed")
	ids, err := collect(rows, func(scanner) (string, error) { return "", broken })
	if !errors.Is(err, broken) || ids != nil {
		t.Fatalf("got %#v, %v, want nil and the scan error", ids, err)
	}
}
