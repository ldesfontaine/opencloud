package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
)

// optionSeparator sépare les noms d'option d'une clé dans la colonne ; aucun
// nom de la liste fermée n'en contient.
const optionSeparator = ","

// ReplaceSSHKeys remplace tout ce que la machine avait par son dernier relevé,
// en une transaction : ce qu'un relevé ne dit plus disparaît.
func (db *DB) ReplaceSSHKeys(ctx context.Context, machineID string, report sshkeys.Report, at time.Time) error {
	return db.inTx(ctx, "replace ssh keys", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ssh_keys WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete ssh keys: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ssh_accounts WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete ssh accounts: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ssh_reports (machine_id, truncated, reported_at) VALUES (?, ?, ?)
			ON CONFLICT (machine_id) DO UPDATE SET truncated = excluded.truncated, reported_at = excluded.reported_at`,
			machineID, report.Truncated, at.Unix())
		if err != nil {
			return fmt.Errorf("upsert ssh report: %w", err)
		}
		for _, account := range report.Accounts {
			if err := insertSSHAccount(ctx, tx, machineID, account); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertSSHAccount(ctx context.Context, tx *sql.Tx, machineID string, account sshkeys.Account) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO ssh_accounts (machine_id, account, uid, state, truncated, ignored) VALUES (?, ?, ?, ?, ?, ?)`,
		machineID, account.Name, account.UID, string(account.State), account.Truncated, account.Ignored)
	if err != nil {
		return fmt.Errorf("insert ssh account %s: %w", account.Name, err)
	}
	for _, key := range account.Keys {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ssh_keys (machine_id, account, file, fingerprint, key_type, bits, comment, options)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			machineID, account.Name, key.File, key.Fingerprint, key.Type, key.Bits, key.Comment,
			strings.Join(key.Options, optionSeparator))
		if err != nil {
			return fmt.Errorf("insert ssh key of %s: %w", account.Name, err)
		}
	}
	return nil
}

// MachineSSHKeys rend le relevé d'une machine, vide et sans date tant que son
// agent n'en a envoyé aucun.
func (db *DB) MachineSSHKeys(ctx context.Context, machineID string) (sshkeys.Snapshot, error) {
	snapshot := sshkeys.Snapshot{MachineID: machineID}
	err := db.sql.QueryRowContext(ctx, `SELECT name FROM machines WHERE id = ?`, machineID).Scan(&snapshot.MachineName)
	if errors.Is(err, sql.ErrNoRows) {
		return sshkeys.Snapshot{}, machine.ErrNotFound
	}
	if err != nil {
		return sshkeys.Snapshot{}, fmt.Errorf("get machine %s: %w", machineID, err)
	}
	snapshots, err := db.readSSHSnapshots(ctx, `WHERE m.id = ?`, machineID)
	if err != nil {
		return sshkeys.Snapshot{}, err
	}
	return snapshots[0], nil
}

// SSHKeyMatrix rend le relevé de chaque machine, par nom.
func (db *DB) SSHKeyMatrix(ctx context.Context) ([]sshkeys.Snapshot, error) {
	return db.readSSHSnapshots(ctx, ``)
}

// readSSHSnapshots lit les machines choisies par where (une clause constante
// de ce fichier) avec leur rapport, leurs comptes et leurs clés : une requête
// par niveau pour toute la liste, regroupées en Go.
func (db *DB) readSSHSnapshots(ctx context.Context, where string, args ...any) ([]sshkeys.Snapshot, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT m.id, m.name, r.reported_at, COALESCE(r.truncated, 0)
		FROM machines m LEFT JOIN ssh_reports r ON r.machine_id = m.id `+where+` ORDER BY m.name, m.id`, args...) // #nosec G202 -- where est une constante de ce fichier.
	if err != nil {
		return nil, fmt.Errorf("list ssh reports: %w", err)
	}
	snapshots, err := collect(rows, scanSSHSnapshot)
	if err != nil {
		return nil, fmt.Errorf("scan ssh reports: %w", err)
	}
	accounts, err := db.readSSHAccounts(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	for index := range snapshots {
		snapshots[index].Accounts = accounts[snapshots[index].MachineID]
		if snapshots[index].Accounts == nil {
			snapshots[index].Accounts = []sshkeys.Account{}
		}
	}
	return snapshots, nil
}

func scanSSHSnapshot(row scanner) (sshkeys.Snapshot, error) {
	var snapshot sshkeys.Snapshot
	var reportedAt sql.NullInt64
	if err := row.Scan(&snapshot.MachineID, &snapshot.MachineName, &reportedAt, &snapshot.Truncated); err != nil {
		return sshkeys.Snapshot{}, err
	}
	if reportedAt.Valid {
		snapshot.ReportedAt = time.Unix(reportedAt.Int64, 0).UTC()
	}
	return snapshot, nil
}

// readSSHAccounts rend les comptes des machines choisies, avec leurs clés,
// par machine.
func (db *DB) readSSHAccounts(ctx context.Context, where string, args ...any) (map[string][]sshkeys.Account, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT a.machine_id, a.account, a.uid, a.state, a.truncated, a.ignored
		FROM ssh_accounts a JOIN machines m ON m.id = a.machine_id `+where+` ORDER BY a.machine_id, a.uid, a.account`, args...) // #nosec G202 -- where est une constante de ce fichier.
	if err != nil {
		return nil, fmt.Errorf("list ssh accounts: %w", err)
	}
	type owned struct {
		machineID string
		account   sshkeys.Account
	}
	listed, err := collect(rows, func(row scanner) (owned, error) {
		var item owned
		var state string
		err := row.Scan(&item.machineID, &item.account.Name, &item.account.UID, &state, &item.account.Truncated, &item.account.Ignored)
		item.account.State = sshkeys.State(state)
		item.account.Keys = []sshkeys.Key{}
		return item, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan ssh accounts: %w", err)
	}
	keys, err := db.readSSHKeys(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	byMachine := map[string][]sshkeys.Account{}
	for _, item := range listed {
		item.account.Keys = append(item.account.Keys, keys[item.machineID+"\x00"+item.account.Name]...)
		byMachine[item.machineID] = append(byMachine[item.machineID], item.account)
	}
	return byMachine, nil
}

// readSSHKeys rend les clés des machines choisies, par (machine, compte), dans
// l'ordre des fichiers puis du commentaire.
func (db *DB) readSSHKeys(ctx context.Context, where string, args ...any) (map[string][]sshkeys.Key, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT k.machine_id, k.account, k.file, k.fingerprint, k.key_type, k.bits, k.comment, k.options
		FROM ssh_keys k JOIN machines m ON m.id = k.machine_id `+where+`
		ORDER BY k.machine_id, k.account, k.file, k.comment, k.fingerprint`, args...) // #nosec G202 -- where est une constante de ce fichier.
	if err != nil {
		return nil, fmt.Errorf("list ssh keys: %w", err)
	}
	type owned struct {
		owner string
		key   sshkeys.Key
	}
	listed, err := collect(rows, func(row scanner) (owned, error) {
		var item owned
		var machineID, account, options string
		err := row.Scan(&machineID, &account, &item.key.File, &item.key.Fingerprint, &item.key.Type, &item.key.Bits, &item.key.Comment, &options)
		item.owner = machineID + "\x00" + account
		item.key.Options = []string{}
		if options != "" {
			item.key.Options = strings.Split(options, optionSeparator)
		}
		return item, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan ssh keys: %w", err)
	}
	byOwner := map[string][]sshkeys.Key{}
	for _, item := range listed {
		byOwner[item.owner] = append(byOwner[item.owner], item.key)
	}
	return byOwner, nil
}
