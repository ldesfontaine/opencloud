package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Zone est une zone Cloudflare enregistrée. Le jeton n'est pas ici : il vit
// dans un fichier à permissions strictes (08-securite-et-secrets.md).
type Zone struct {
	Name         string
	CloudflareID string
	AddedAt      time.Time
	RotatedAt    time.Time
}

// ZoneMachine dit où le jeton d'une zone est posé, et lequel. Fingerprint est
// l'empreinte que le script a écrite ; jamais le jeton.
type ZoneMachine struct {
	Zone        string
	MachineID   string
	PlacedAt    time.Time
	Fingerprint string
}

const (
	zoneColumns        = `name, cloudflare_id, added_at, rotated_at`
	zoneMachineColumns = `zone, machine_id, placed_at, fingerprint`
)

// Zones rend les zones enregistrées, dans l'ordre où on les lit.
func (s *Store) Zones(ctx context.Context) ([]Zone, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+zoneColumns+` FROM zones ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	defer rows.Close()

	var zones []Zone
	for rows.Next() {
		zone, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		zones = append(zones, zone)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	return zones, nil
}

// Zone rend une zone par son nom, ou ErrNotFound.
func (s *Store) Zone(ctx context.Context, name string) (Zone, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+zoneColumns+` FROM zones WHERE name = ?`, name)
	return scanZone(row)
}

// InsertZone enregistre une zone. Une zone déjà enregistrée est un conflit :
// c'est la rotation qui remplace son jeton, pas un second ajout.
func (s *Store) InsertZone(ctx context.Context, zone Zone) error {
	moment := formatTime(zone.AddedAt)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO zones (`+zoneColumns+`)
		VALUES (?, ?, ?, ?)`,
		zone.Name, zone.CloudflareID, moment, moment)
	if err != nil {
		return fmt.Errorf("insert zone: %w", err)
	}
	return nil
}

// MarkZoneRotated note la date du nouveau jeton, et met à jour l'identifiant
// Cloudflare : un jeton neuf a été vérifié sur la zone, elle vient d'être
// relue.
func (s *Store) MarkZoneRotated(ctx context.Context, name, cloudflareID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE zones SET cloudflare_id = ?, rotated_at = ? WHERE name = ?`,
		cloudflareID, formatTime(at), name)
	if err != nil {
		return fmt.Errorf("mark zone rotated: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark zone rotated: %w", err)
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteZone retire une zone et, par la clé étrangère, les machines qui la
// portaient.
func (s *Store) DeleteZone(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM zones WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete zone: %w", err)
	}
	return nil
}

// ZoneMachines rend les machines qui portent le jeton d'une zone.
func (s *Store) ZoneMachines(ctx context.Context, zone string) ([]ZoneMachine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+zoneMachineColumns+` FROM zone_machines WHERE zone = ? ORDER BY machine_id`, zone)
	if err != nil {
		return nil, fmt.Errorf("list zone machines: %w", err)
	}
	defer rows.Close()

	var placements []ZoneMachine
	for rows.Next() {
		placement, err := scanZoneMachine(rows)
		if err != nil {
			return nil, err
		}
		placements = append(placements, placement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list zone machines: %w", err)
	}
	return placements, nil
}

// RecordZoneMachine écrit ce qu'une machine porte. Une pose rejouée met la
// ligne à jour, elle n'en crée pas une seconde.
func (s *Store) RecordZoneMachine(ctx context.Context, placement ZoneMachine) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO zone_machines (`+zoneMachineColumns+`)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (zone, machine_id) DO UPDATE SET
			placed_at = excluded.placed_at,
			fingerprint = excluded.fingerprint`,
		placement.Zone, placement.MachineID, formatTime(placement.PlacedAt), placement.Fingerprint)
	if err != nil {
		return fmt.Errorf("record zone machine: %w", err)
	}
	return nil
}

func scanZone(row scanner) (Zone, error) {
	var zone Zone
	var addedAt, rotatedAt string
	err := row.Scan(&zone.Name, &zone.CloudflareID, &addedAt, &rotatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	if err != nil {
		return Zone{}, fmt.Errorf("scan zone: %w", err)
	}

	if zone.AddedAt, err = parseTime(addedAt); err != nil {
		return Zone{}, err
	}
	if zone.RotatedAt, err = parseTime(rotatedAt); err != nil {
		return Zone{}, err
	}
	return zone, nil
}

func scanZoneMachine(row scanner) (ZoneMachine, error) {
	var placement ZoneMachine
	var placedAt string
	if err := row.Scan(&placement.Zone, &placement.MachineID, &placedAt, &placement.Fingerprint); err != nil {
		return ZoneMachine{}, fmt.Errorf("scan zone machine: %w", err)
	}

	var err error
	if placement.PlacedAt, err = parseTime(placedAt); err != nil {
		return ZoneMachine{}, err
	}
	return placement, nil
}
