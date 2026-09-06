package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session est une session ouverte. TokenHash est l'empreinte du jeton, jamais
// le jeton lui-même.
type Session struct {
	TokenHash string
	AccountID int64
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, account_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		session.TokenHash, session.AccountID,
		session.CreatedAt.UTC().Format(time.RFC3339), session.ExpiresAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) FindSession(ctx context.Context, tokenHash string) (Session, error) {
	var session Session
	var createdAt, expiresAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, account_id, created_at, expires_at
		FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&session.TokenHash, &session.AccountID, &createdAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("scan session: %w", err)
	}

	session.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt, err = parseTime(expiresAt)
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteSessionsOfAccount ferme toutes les sessions d'un compte, par exemple
// après un changement de mot de passe.
func (s *Store) DeleteSessionsOfAccount(ctx context.Context, accountID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID)
	if err != nil {
		return fmt.Errorf("delete sessions of account: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`,
		now.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
