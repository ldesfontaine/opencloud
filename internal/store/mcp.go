package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// --- Client ---

func (db *DB) GetMCPClient(ctx context.Context) (mcp.Client, error) {
	var client mcp.Client
	var uris string
	var createdAt int64
	err := db.sql.QueryRowContext(ctx,
		`SELECT id, secret_hash, secret_prefix, redirect_uris, created_at FROM mcp_clients LIMIT 1`,
	).Scan(&client.ID, &client.SecretHash, &client.SecretPrefix, &uris, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return mcp.Client{}, mcp.ErrNotFound
	}
	if err != nil {
		return mcp.Client{}, fmt.Errorf("get mcp client: %w", err)
	}
	client.RedirectURIs = splitLines(uris)
	client.CreatedAt = time.Unix(createdAt, 0)
	return client, nil
}

func (db *DB) InsertMCPClient(ctx context.Context, client mcp.Client) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO mcp_clients (id, secret_hash, secret_prefix, redirect_uris, created_at) VALUES (?, ?, ?, ?, ?)`,
		client.ID, client.SecretHash, client.SecretPrefix, strings.Join(client.RedirectURIs, "\n"), client.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert mcp client: %w", err)
	}
	return nil
}

func (db *DB) UpdateMCPClientSecret(ctx context.Context, hash, prefix string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE mcp_clients SET secret_hash = ?, secret_prefix = ?`, hash, prefix)
	if err != nil {
		return fmt.Errorf("update mcp client secret: %w", err)
	}
	return mcpNotFoundIfNoRow(result)
}

func (db *DB) UpdateMCPClientRedirectURIs(ctx context.Context, uris []string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE mcp_clients SET redirect_uris = ?`, strings.Join(uris, "\n"))
	if err != nil {
		return fmt.Errorf("update mcp client redirect uris: %w", err)
	}
	return mcpNotFoundIfNoRow(result)
}

func (db *DB) DeleteMCPClient(ctx context.Context) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM mcp_clients`)
	if err != nil {
		return fmt.Errorf("delete mcp client: %w", err)
	}
	return mcpNotFoundIfNoRow(result)
}

// --- Codes ---

func (db *DB) InsertMCPCode(ctx context.Context, code mcp.Code) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO mcp_codes (code_hash, client_id, redirect_uri, challenge, expires_at, used, created_at)
		VALUES (?, ?, ?, ?, ?, 0, ?)`,
		code.Hash, code.ClientID, code.RedirectURI, code.Challenge, code.ExpiresAt.Unix(), code.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert mcp code: %w", err)
	}
	return nil
}

// ConsumeMCPCode marque le code servi, en une seule mise à jour
// conditionnelle : de deux échanges concurrents, un seul touche la ligne.
// Absent, périmé ou déjà servi, c'est le même refus : rien ne dit à un
// client ce qu'il a deviné.
func (db *DB) ConsumeMCPCode(ctx context.Context, hash string, now time.Time) (mcp.Code, error) {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE mcp_codes SET used = 1 WHERE code_hash = ? AND used = 0 AND expires_at > ?`, hash, now.Unix())
	if err != nil {
		return mcp.Code{}, fmt.Errorf("consume mcp code: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return mcp.Code{}, err
	}
	if affected != 1 {
		return mcp.Code{}, mcp.ErrCodeInvalid
	}
	var code mcp.Code
	var expiresAt, createdAt int64
	var used int
	err = db.sql.QueryRowContext(ctx,
		`SELECT code_hash, client_id, redirect_uri, challenge, expires_at, used, created_at FROM mcp_codes WHERE code_hash = ?`, hash,
	).Scan(&code.Hash, &code.ClientID, &code.RedirectURI, &code.Challenge, &expiresAt, &used, &createdAt)
	if err != nil {
		return mcp.Code{}, fmt.Errorf("read mcp code: %w", err)
	}
	code.ExpiresAt = time.Unix(expiresAt, 0)
	code.CreatedAt = time.Unix(createdAt, 0)
	code.Used = used != 0
	return code, nil
}

// --- Jetons ---

const mcpTokenColumns = `id, token_hash, token_prefix, kind, name, family_id, created_at, expires_at, last_used_at, revoked_at` // #nosec G101 -- des noms de colonnes.

func (db *DB) InsertMCPToken(ctx context.Context, token mcp.Token) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, name, family_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		token.ID, token.Hash, token.Prefix, string(token.Kind), token.Name, token.FamilyID, token.CreatedAt.Unix(), nullableTime(token.ExpiresAt))
	if err != nil {
		return fmt.Errorf("insert mcp token: %w", err)
	}
	return nil
}

func (db *DB) GetMCPToken(ctx context.Context, hash string) (mcp.Token, error) {
	token, err := scanMCPToken(db.sql.QueryRowContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return mcp.Token{}, mcp.ErrNotFound
	}
	if err != nil {
		return mcp.Token{}, fmt.Errorf("get mcp token: %w", err)
	}
	return token, nil
}

// ConsumeMCPRefreshToken révoque le jeton de rafraîchissement en une
// mise à jour conditionnelle : un seul rafraîchissement concurrent le
// consomme. Un jeton déjà révoqué est un rejeu, et la ligne est rendue
// avec l'erreur pour que l'appelant révoque sa famille ; inconnu ou
// périmé, c'est un refus sec.
func (db *DB) ConsumeMCPRefreshToken(ctx context.Context, hash string, now time.Time) (mcp.Token, error) {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE token_hash = ? AND kind = ? AND revoked_at IS NULL AND expires_at > ?`,
		now.Unix(), hash, string(mcp.KindRefresh), now.Unix())
	if err != nil {
		return mcp.Token{}, fmt.Errorf("consume mcp refresh token: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return mcp.Token{}, err
	}
	token, err := db.GetMCPToken(ctx, hash)
	if errors.Is(err, mcp.ErrNotFound) {
		return mcp.Token{}, mcp.ErrTokenInvalid
	}
	if err != nil {
		return mcp.Token{}, err
	}
	if affected == 1 {
		return token, nil
	}
	if token.Kind != mcp.KindRefresh || token.IsExpired(now) {
		return mcp.Token{}, mcp.ErrTokenInvalid
	}
	// La ligne existe, vivante, et ce n'est pas nous qui l'avons révoquée :
	// elle l'était déjà, ou un concurrent vient de gagner. Dans les deux
	// cas, elle a servi deux fois.
	return token, mcp.ErrTokenReplayed
}

func (db *DB) TouchMCPToken(ctx context.Context, id string, at time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `UPDATE mcp_tokens SET last_used_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		return fmt.Errorf("touch mcp token: %w", err)
	}
	return nil
}

func (db *DB) RevokeMCPToken(ctx context.Context, id string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("revoke mcp token: %w", err)
	}
	return mcpNotFoundIfNoRow(result)
}

func (db *DB) RevokeMCPFamily(ctx context.Context, familyID string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE family_id = ? AND family_id != '' AND revoked_at IS NULL`, at.Unix(), familyID)
	if err != nil {
		return fmt.Errorf("revoke mcp family: %w", err)
	}
	return mcpNotFoundIfNoRow(result)
}

// RevokeMCPOAuthTokens révoque tout ce qui vient d'une autorisation OAuth ;
// les jetons d'API restent.
func (db *DB) RevokeMCPOAuthTokens(ctx context.Context, at time.Time) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE mcp_tokens SET revoked_at = ? WHERE kind != ? AND revoked_at IS NULL`, at.Unix(), string(mcp.KindAPI))
	if err != nil {
		return fmt.Errorf("revoke mcp oauth tokens: %w", err)
	}
	return nil
}

func (db *DB) DeleteAllMCPTokens(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM mcp_tokens`); err != nil {
		return fmt.Errorf("delete mcp tokens: %w", err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM mcp_codes`); err != nil {
		return fmt.Errorf("delete mcp codes: %w", err)
	}
	return nil
}

// ListMCPTokens rend les jetons vivants d'une sorte, les plus récents d'abord.
func (db *DB) ListMCPTokens(ctx context.Context, kind mcp.Kind) ([]mcp.Token, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT `+mcpTokenColumns+` FROM mcp_tokens WHERE kind = ? AND revoked_at IS NULL ORDER BY created_at DESC, id`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("list mcp tokens: %w", err)
	}
	defer rows.Close()
	tokens := []mcp.Token{}
	for rows.Next() {
		token, err := scanMCPToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// ListMCPSessions rend les familles dont un jeton de rafraîchissement est
// encore vivant : quand elle a commencé, quand un accès a servi pour la
// dernière fois, quand elle expire sans rafraîchissement.
func (db *DB) ListMCPSessions(ctx context.Context, now time.Time) ([]mcp.Session, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT family_id, MIN(created_at), MAX(COALESCE(last_used_at, 0)), MAX(expires_at)
		FROM mcp_tokens
		WHERE family_id != '' AND family_id IN (
			SELECT family_id FROM mcp_tokens WHERE kind = ? AND revoked_at IS NULL AND expires_at > ?
		)
		GROUP BY family_id ORDER BY MIN(created_at) DESC, family_id`, string(mcp.KindRefresh), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list mcp sessions: %w", err)
	}
	defer rows.Close()
	sessions := []mcp.Session{}
	for rows.Next() {
		var session mcp.Session
		var startedAt, lastUsedAt, expiresAt int64
		if err := rows.Scan(&session.FamilyID, &startedAt, &lastUsedAt, &expiresAt); err != nil {
			return nil, err
		}
		session.StartedAt = time.Unix(startedAt, 0)
		session.ExpiresAt = time.Unix(expiresAt, 0)
		if lastUsedAt != 0 {
			session.LastUsedAt = time.Unix(lastUsedAt, 0)
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// PurgeMCPExpired efface les codes et jetons échus, révoqués ou non : un
// révoqué non échu reste, c'est lui qui trahit un rejeu.
func (db *DB) PurgeMCPExpired(ctx context.Context, before time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM mcp_codes WHERE expires_at <= ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge mcp codes: %w", err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM mcp_tokens WHERE expires_at IS NOT NULL AND expires_at <= ?`, before.Unix()); err != nil {
		return fmt.Errorf("purge mcp tokens: %w", err)
	}
	return nil
}

func scanMCPToken(row scanner) (mcp.Token, error) {
	var token mcp.Token
	var kind string
	var createdAt int64
	var expiresAt, lastUsedAt, revokedAt sql.NullInt64
	err := row.Scan(&token.ID, &token.Hash, &token.Prefix, &kind, &token.Name, &token.FamilyID, &createdAt, &expiresAt, &lastUsedAt, &revokedAt)
	if err != nil {
		return mcp.Token{}, err
	}
	token.Kind = mcp.Kind(kind)
	token.CreatedAt = time.Unix(createdAt, 0)
	token.ExpiresAt = timeOf(expiresAt)
	token.LastUsedAt = timeOf(lastUsedAt)
	token.RevokedAt = timeOf(revokedAt)
	return token, nil
}

func mcpNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return mcp.ErrNotFound
	}
	return nil
}

func splitLines(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}
