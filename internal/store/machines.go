package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LocalMachineID est la machine openCloud, insérée par la migration 003 : le
// binaire tourne dessus, il n'y a rien à déclarer pour qu'elle existe.
const LocalMachineID = "local"

// Ce que la sonde « Tester l'accès » a constaté. Une chaîne vide dit qu'elle
// n'a encore rien constaté, ce qui n'est pas « injoignable » (02-roles.md).
const (
	ProbeReachable      = "reachable"
	ProbeSSHFailed      = "ssh-failed"
	ProbeLauncherFailed = "launcher-failed"
)

// Machine est une machine de l'infrastructure. Address et Port sont ceux du
// SSH ; Account le compte dédié posé à l'enrôlement ; les champs Probe* sont
// la dernière remontée de la sonde, datée telle quelle.
type Machine struct {
	ID         string
	Name       string
	Address    string
	Port       int
	Account    string
	CreatedAt  time.Time
	ProbedAt   time.Time
	ProbeState string
	ProbeNote  string
}

const machineColumns = `id, name, address, port, account, created_at,
	probed_at, probe_state, probe_note`

func (s *Store) Machines(ctx context.Context) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+machineColumns+` FROM machines ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	defer rows.Close()

	var machines []Machine
	for rows.Next() {
		machine, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		machines = append(machines, machine)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	return machines, nil
}

func (s *Store) Machine(ctx context.Context, id string) (Machine, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+machineColumns+` FROM machines WHERE id = ?`, id)
	machine, err := scanMachine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, ErrNotFound
	}
	return machine, err
}

// MachineExists évite de lire toute la ligne quand la question est seulement
// « cet identifiant est-il déjà pris ».
func (s *Store) MachineExists(ctx context.Context, id string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM machines WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check machine: %w", err)
	}
	return true, nil
}

// InsertMachine déclare une machine. La sonde ne l'a encore jamais vue : ses
// colonnes restent vides jusqu'à la première remontée.
func (s *Store) InsertMachine(ctx context.Context, machine Machine) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO machines (id, name, address, port, account, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		machine.ID, machine.Name, machine.Address, machine.Port, machine.Account,
		formatTime(machine.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert machine: %w", err)
	}
	return nil
}

// UpdateMachineAccess change l'adresse et le port par lesquels openCloud joint
// la machine — l'action nommée « changer adresse, port ou empreinte ».
func (s *Store) UpdateMachineAccess(ctx context.Context, id, address string, port int) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE machines SET address = ?, port = ? WHERE id = ?`, address, port, id)
	if err != nil {
		return fmt.Errorf("update machine access: %w", err)
	}
	return checkOneRow(result, "update machine access")
}

// RecordProbe écrit ce que la sonde a constaté. Elle tourne en silence : elle
// n'écrit rien d'autre, et jamais dans le journal des actions.
func (s *Store) RecordProbe(ctx context.Context, id string, at time.Time, state, note string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE machines SET probed_at = ?, probe_state = ?, probe_note = ? WHERE id = ?`,
		formatTime(at), state, note, id)
	if err != nil {
		return fmt.Errorf("record probe: %w", err)
	}
	return checkOneRow(result, "record probe")
}

func checkOneRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner : *sql.Row et *sql.Rows ont le même Scan, une seule lecture suffit.
type scanner interface {
	Scan(destinations ...any) error
}

func scanMachine(row scanner) (Machine, error) {
	var machine Machine
	var createdAt, probedAt string
	err := row.Scan(&machine.ID, &machine.Name, &machine.Address,
		&machine.Port, &machine.Account, &createdAt,
		&probedAt, &machine.ProbeState, &machine.ProbeNote)
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, err
	}
	if err != nil {
		return Machine{}, fmt.Errorf("scan machine: %w", err)
	}

	machine.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Machine{}, err
	}
	// Jamais sondée : la date reste nulle, il n'y a pas de « dernière remontée ».
	if probedAt != "" {
		machine.ProbedAt, err = parseTime(probedAt)
		if err != nil {
			return Machine{}, err
		}
	}
	return machine, nil
}
