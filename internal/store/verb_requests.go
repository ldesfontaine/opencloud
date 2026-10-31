package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/verbrequest"
)

var _ verbrequest.Store = (*DB)(nil)

const verbRequestColumns = `id, verb, object_name, params, body, token_id, token_name, on_behalf_of, state, created_at, expires_at`

func (db *DB) InsertVerbRequest(ctx context.Context, request verbrequest.Request) error {
	params, err := json.Marshal(request.Params)
	if err != nil {
		return fmt.Errorf("encode verb request params: %w", err)
	}
	_, err = db.sql.ExecContext(ctx, `INSERT INTO verb_requests (`+verbRequestColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID, request.Verb, request.ObjectName, string(params), request.Body, request.TokenID, request.TokenName,
		request.OnBehalfOf, string(request.State), request.CreatedAt.Unix(), request.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("insert verb request: %w", err)
	}
	return nil
}

// FindPendingVerbRequest compare les paramètres tels qu'ils sont écrits : le
// JSON d'une table de chaînes sort ses clés dans l'ordre, deux demandes
// identiques s'écrivent à l'identique.
func (db *DB) FindPendingVerbRequest(ctx context.Context, request verbrequest.Request, now time.Time) (verbrequest.Request, error) {
	params, err := json.Marshal(request.Params)
	if err != nil {
		return verbrequest.Request{}, fmt.Errorf("encode verb request params: %w", err)
	}
	return queryVerbRequest(db.sql.QueryRowContext(ctx, `
		SELECT `+verbRequestColumns+` FROM verb_requests
		WHERE token_id = ? AND verb = ? AND params = ? AND body = ? AND state = ? AND expires_at > ?`,
		request.TokenID, request.Verb, string(params), request.Body, string(verbrequest.StateAwaiting), now.Unix()))
}

func (db *DB) CountPendingVerbRequests(ctx context.Context, tokenID string, now time.Time) (int, error) {
	var count int
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM verb_requests WHERE token_id = ? AND state = ? AND expires_at > ?`,
		tokenID, string(verbrequest.StateAwaiting), now.Unix()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending verb requests: %w", err)
	}
	return count, nil
}

func (db *DB) ListPendingVerbRequests(ctx context.Context, now time.Time) ([]verbrequest.Request, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+verbRequestColumns+` FROM verb_requests
		WHERE state = ? AND expires_at > ? ORDER BY created_at, id`,
		string(verbrequest.StateAwaiting), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list pending verb requests: %w", err)
	}
	return collect(rows, scanVerbRequest)
}

func (db *DB) GetVerbRequest(ctx context.Context, id string) (verbrequest.Request, error) {
	return queryVerbRequest(db.sql.QueryRowContext(ctx, `SELECT `+verbRequestColumns+` FROM verb_requests WHERE id = ?`, id))
}

func (db *DB) DecideVerbRequest(ctx context.Context, id string, state verbrequest.State, now time.Time) (verbrequest.Request, error) {
	var decided verbrequest.Request
	err := db.inTx(ctx, "decide verb request", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE verb_requests SET state = ? WHERE id = ? AND state = ? AND expires_at > ?`,
			string(state), id, string(verbrequest.StateAwaiting), now.Unix())
		if err != nil {
			return fmt.Errorf("decide verb request: %w", err)
		}
		if err := notFoundIfNoRow(result, verbrequest.ErrNotFound); err != nil {
			return err
		}
		decided, err = queryVerbRequest(tx.QueryRowContext(ctx, `SELECT `+verbRequestColumns+` FROM verb_requests WHERE id = ?`, id))
		return err
	})
	return decided, err
}

func (db *DB) ExpireDueVerbRequests(ctx context.Context, now time.Time) ([]verbrequest.Request, error) {
	var expired []verbrequest.Request
	err := db.inTx(ctx, "expire verb requests", func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+verbRequestColumns+` FROM verb_requests WHERE state = ? AND expires_at <= ? ORDER BY created_at, id`,
			string(verbrequest.StateAwaiting), now.Unix())
		if err != nil {
			return fmt.Errorf("list due verb requests: %w", err)
		}
		expired, err = collect(rows, scanVerbRequest)
		if err != nil {
			return err
		}
		if len(expired) == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE verb_requests SET state = ? WHERE state = ? AND expires_at <= ?`,
			string(verbrequest.StateExpired), string(verbrequest.StateAwaiting), now.Unix()); err != nil {
			return fmt.Errorf("expire verb requests: %w", err)
		}
		return nil
	})
	return expired, err
}

// PurgeVerbRequests efface les demandes terminées dont l'échéance date d'avant
// before. Une demande en attente a une échéance proche : elle n'est jamais là.
func (db *DB) PurgeVerbRequests(ctx context.Context, before time.Time) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM verb_requests WHERE state <> ? AND expires_at < ?`,
		string(verbrequest.StateAwaiting), before.Unix())
	if err != nil {
		return fmt.Errorf("purge verb requests: %w", err)
	}
	return nil
}

func queryVerbRequest(row *sql.Row) (verbrequest.Request, error) {
	request, err := scanVerbRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return verbrequest.Request{}, verbrequest.ErrNotFound
	}
	return request, err
}

func scanVerbRequest(row scanner) (verbrequest.Request, error) {
	var (
		request   verbrequest.Request
		params    string
		state     string
		createdAt int64
		expiresAt int64
	)
	if err := row.Scan(&request.ID, &request.Verb, &request.ObjectName, &params, &request.Body, &request.TokenID,
		&request.TokenName, &request.OnBehalfOf, &state, &createdAt, &expiresAt); err != nil {
		return verbrequest.Request{}, fmt.Errorf("scan verb request: %w", err)
	}
	if err := json.Unmarshal([]byte(params), &request.Params); err != nil {
		return verbrequest.Request{}, fmt.Errorf("decode verb request params: %w", err)
	}
	request.State = verbrequest.State(state)
	request.CreatedAt = time.Unix(createdAt, 0)
	request.ExpiresAt = time.Unix(expiresAt, 0)
	return request, nil
}
