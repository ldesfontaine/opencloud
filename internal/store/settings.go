package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

const settingsSelect = `SELECT language, status_title, status_announcement, status_language,
	disk_attention_percent, disk_danger_percent,
	mail_host, mail_port, mail_tls, mail_username, mail_password, mail_from,
	lifecycle_disabled
	FROM settings WHERE id = 1`

// LoadSettings rend les réglages de l'opérateur ; sans ligne, des réglages
// vides : une installation neuve n'a encore rien réglé.
func (db *DB) LoadSettings(ctx context.Context) (settings.Settings, error) {
	current, err := db.scanSettings(db.sql.QueryRowContext(ctx, settingsSelect))
	if errors.Is(err, sql.ErrNoRows) {
		return settings.Settings{}, nil
	}
	if err != nil {
		return settings.Settings{}, fmt.Errorf("load settings: %w", err)
	}
	return current, nil
}

// UpdateSettings relit, applique change et réécrit dans une seule
// transaction : deux écrivains, même de deux processus, ne s'effacent pas
// leurs clés. change ne touche que les siennes. Renvoie les réglages écrits.
func (db *DB) UpdateSettings(ctx context.Context, change func(*settings.Settings)) (settings.Settings, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return settings.Settings{}, fmt.Errorf("begin settings update: %w", err)
	}
	defer tx.Rollback()

	current, err := db.scanSettings(tx.QueryRowContext(ctx, settingsSelect))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return settings.Settings{}, fmt.Errorf("load settings: %w", err)
	}
	change(&current)
	if err := db.saveSettings(ctx, tx, current, time.Now()); err != nil {
		return settings.Settings{}, err
	}
	if err := tx.Commit(); err != nil {
		return settings.Settings{}, fmt.Errorf("commit settings update: %w", err)
	}
	return current, nil
}

// ImportSettings écrit des réglages repris d'ailleurs, seulement si la base
// n'en a pas encore : ce qui y est déjà a été réglé depuis. Dit s'il a écrit.
func (db *DB) ImportSettings(ctx context.Context, imported settings.Settings) (bool, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin settings import: %w", err)
	}
	defer tx.Rollback()

	var present int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&present)
	if err != nil {
		return false, fmt.Errorf("count settings: %w", err)
	}
	if present > 0 {
		return false, nil
	}
	if err := db.saveSettings(ctx, tx, imported, time.Now()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit settings import: %w", err)
	}
	return true, nil
}

func (db *DB) scanSettings(row scanner) (settings.Settings, error) {
	var current settings.Settings
	var language, statusLanguage string
	var lifecycleDisabled int
	err := row.Scan(&language, &current.StatusTitle, &current.StatusAnnouncement, &statusLanguage,
		&current.DiskAttentionPercent, &current.DiskDangerPercent,
		&current.MailHost, &current.MailPort, &current.MailTLS, &current.MailUsername, &current.MailPassword, &current.MailFrom,
		&lifecycleDisabled)
	if err != nil {
		return settings.Settings{}, err
	}
	current.MailPassword, err = db.openSecret(current.MailPassword, settingsPasswordScope())
	if err != nil {
		return settings.Settings{}, err
	}
	current.Language = lang.Code(language)
	current.StatusLanguage = lang.Code(statusLanguage)
	current.LifecycleDisabled = lifecycleDisabled != 0
	return current, nil
}

// saveSettings remplace la ligne unique, créée au premier appel.
func (db *DB) saveSettings(ctx context.Context, tx *sql.Tx, current settings.Settings, at time.Time) error {
	mailPassword, err := db.sealSecret(current.MailPassword, settingsPasswordScope())
	if err != nil {
		return fmt.Errorf("seal mail password: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO settings (id, language, status_title, status_announcement, status_language,
			disk_attention_percent, disk_danger_percent,
			mail_host, mail_port, mail_tls, mail_username, mail_password, mail_from,
			lifecycle_disabled, updated_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			language = excluded.language,
			status_title = excluded.status_title,
			status_announcement = excluded.status_announcement,
			status_language = excluded.status_language,
			disk_attention_percent = excluded.disk_attention_percent,
			disk_danger_percent = excluded.disk_danger_percent,
			mail_host = excluded.mail_host,
			mail_port = excluded.mail_port,
			mail_tls = excluded.mail_tls,
			mail_username = excluded.mail_username,
			mail_password = excluded.mail_password,
			mail_from = excluded.mail_from,
			lifecycle_disabled = excluded.lifecycle_disabled,
			updated_at = excluded.updated_at`,
		string(current.Language), current.StatusTitle, current.StatusAnnouncement, string(current.StatusLanguage),
		current.DiskAttentionPercent, current.DiskDangerPercent,
		current.MailHost, current.MailPort, current.MailTLS, current.MailUsername, mailPassword, current.MailFrom,
		boolToInt(current.LifecycleDisabled), at.Unix())
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
