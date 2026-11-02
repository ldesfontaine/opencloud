package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
)

const linkColumns = `id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, port, first_seen_at, last_seen_at`

// countLinksOf compte les liens dont une extrémité est sur la machine.
const countLinksOf = `SELECT COUNT(*) FROM links WHERE from_machine_id = ?1 OR to_machine_id = ?1`

// InsertLink écrit un lien de l'opérateur. Lire la paire et le plafond avant
// d'écrire donne le refus précis, là où un index violé ne dirait rien.
func (db *DB) InsertLink(ctx context.Context, written link.Link) error {
	return db.inTx(ctx, "insert link", func(tx *sql.Tx) error {
		var pairs int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM links WHERE from_machine_id = ? AND from_key = ? AND to_machine_id = ? AND to_key = ? AND nature = ?`,
			written.From.MachineID, written.From.Key, written.To.MachineID, written.To.Key, string(written.Nature)).Scan(&pairs)
		if err != nil {
			return fmt.Errorf("find link pair: %w", err)
		}
		if pairs > 0 {
			return link.ErrDuplicate
		}
		for _, machineID := range []string{written.From.MachineID, written.To.MachineID} {
			var count int
			if err := tx.QueryRowContext(ctx, countLinksOf, machineID).Scan(&count); err != nil {
				return fmt.Errorf("count links: %w", err)
			}
			if count >= link.MaxLinksPerMachine {
				return link.ErrTooMany
			}
		}
		return insertLink(ctx, tx, written)
	})
}

func insertLink(ctx context.Context, to execer, written link.Link) error {
	_, err := to.ExecContext(ctx,
		`INSERT INTO links (`+linkColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		written.ID, written.From.MachineID, written.From.Key, written.To.MachineID, written.To.Key,
		string(written.Nature), string(written.Proof), string(written.Signal), written.Port,
		written.FirstSeen.Unix(), written.LastSeen.Unix())
	if err != nil {
		return fmt.Errorf("insert link: %w", err)
	}
	return nil
}

func (db *DB) GetLink(ctx context.Context, id string) (link.Link, error) {
	found, err := scanLink(db.sql.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM links WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return link.Link{}, link.ErrNotFound
	}
	if err != nil {
		return link.Link{}, fmt.Errorf("get link: %w", err)
	}
	return found, nil
}

func (db *DB) DeleteLink(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	if removed, err := result.RowsAffected(); err == nil && removed == 0 {
		return link.ErrNotFound
	}
	return nil
}

// ListLinks rend les liens dont une extrémité est sur la machine, tous si
// machineID est vide.
func (db *DB) ListLinks(ctx context.Context, machineID string) ([]link.Link, error) {
	query := `SELECT ` + linkColumns + ` FROM links`
	var args []any
	if machineID != "" {
		query += ` WHERE from_machine_id = ?1 OR to_machine_id = ?1`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY first_seen_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}
	return collect(rows, scanLink)
}

// TouchSeenLinks avance la dernière vue des liens connus et crée les neufs,
// jusqu'au plafond de la machine ; ce qui dépasse est écarté, le rapport
// passe. Aucun lien n'est retiré : ne plus voir n'est pas supprimer. Un lien
// que l'opérateur a écrit sur la même paire et la même nature reste tel quel,
// et aucune ligne ne naît à côté : l'opérateur a le dernier mot.
func (db *DB) TouchSeenLinks(ctx context.Context, machineID string, links []link.Link) error {
	return db.inTx(ctx, "touch seen links", func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, countLinksOf, machineID).Scan(&count); err != nil {
			return fmt.Errorf("count links: %w", err)
		}
		const pair = `from_machine_id = ? AND from_key = ? AND to_machine_id = ? AND to_key = ? AND nature = ?`
		for _, seen := range links {
			pairArgs := []any{seen.From.MachineID, seen.From.Key, seen.To.MachineID, seen.To.Key, string(seen.Nature)}
			result, err := tx.ExecContext(ctx,
				`UPDATE links SET last_seen_at = ?, signal = ?, port = CASE WHEN ? > 0 THEN ? ELSE port END
				 WHERE `+pair+` AND signal <> ?`,
				append([]any{seen.LastSeen.Unix(), string(seen.Signal), seen.Port, seen.Port}, append(pairArgs, string(link.SignalOperator))...)...)
			if err != nil {
				return fmt.Errorf("touch link: %w", err)
			}
			if touched, err := result.RowsAffected(); err == nil && touched > 0 {
				continue
			}
			var written int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM links WHERE `+pair, pairArgs...).Scan(&written); err != nil {
				return fmt.Errorf("find link pair: %w", err)
			}
			if written > 0 || count >= link.MaxLinksPerMachine {
				continue
			}
			if err := insertLink(ctx, tx, seen); err != nil {
				return err
			}
			count++
		}
		return nil
	})
}

func scanLink(row scanner) (link.Link, error) {
	var found link.Link
	var nature, proof, signal string
	var first, last int64
	err := row.Scan(&found.ID, &found.From.MachineID, &found.From.Key, &found.To.MachineID, &found.To.Key,
		&nature, &proof, &signal, &found.Port, &first, &last)
	if err != nil {
		return link.Link{}, err
	}
	found.Nature, found.Proof, found.Signal = link.Nature(nature), link.Proof(proof), link.Signal(signal)
	found.FirstSeen, found.LastSeen = time.Unix(first, 0).UTC(), time.Unix(last, 0).UTC()
	return found, nil
}
