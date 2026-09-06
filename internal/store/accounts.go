package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Account est un compte de l'interface. Les colonnes TOTP existent en base
// mais ne sont pas encore portées ici : rien ne les lit.
type Account struct {
	ID                 int64
	Username           string
	PasswordHash       string
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count accounts: %w", err)
	}
	return count, nil
}

// CreateAccount insère le compte et rend son identifiant.
func (s *Store) CreateAccount(ctx context.Context, account Account) (int64, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (username, password_hash, must_change_password, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		account.Username, account.PasswordHash, account.MustChangePassword,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("insert account: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read account id: %w", err)
	}
	return id, nil
}

func (s *Store) FindAccountByUsername(ctx context.Context, username string) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, must_change_password, created_at, updated_at
		FROM accounts WHERE username = ?`, username)
	return scanAccount(row)
}

func (s *Store) FindAccountByID(ctx context.Context, id int64) (Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, must_change_password, created_at, updated_at
		FROM accounts WHERE id = ?`, id)
	return scanAccount(row)
}

// UpdateAccountPassword remplace l'empreinte et lève ou pose l'obligation de changement.
func (s *Store) UpdateAccountPassword(ctx context.Context, id int64, passwordHash string, mustChange bool) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET password_hash = ?, must_change_password = ?, updated_at = ?
		WHERE id = ?`,
		passwordHash, mustChange, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("update account password: %w", err)
	}
	return oneRowAffected(result)
}

// SetMustChangePassword pose ou lève l'obligation, sans toucher au mot de passe.
func (s *Store) SetMustChangePassword(ctx context.Context, id int64, mustChange bool) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET must_change_password = ?, updated_at = ? WHERE id = ?`,
		mustChange, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("set must_change_password: %w", err)
	}
	return oneRowAffected(result)
}

func scanAccount(row *sql.Row) (Account, error) {
	var account Account
	var createdAt, updatedAt string
	err := row.Scan(&account.ID, &account.Username, &account.PasswordHash,
		&account.MustChangePassword, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("scan account: %w", err)
	}

	account.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Account{}, err
	}
	account.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

func oneRowAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count affected rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Les dates sont stockées en texte RFC 3339 UTC : lisibles dans la base,
// comparables par SQLite.
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", value, err)
	}
	return parsed, nil
}
