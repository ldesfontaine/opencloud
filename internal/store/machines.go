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

// Machine est une machine de l'infrastructure. Address et Port sont ceux du
// SSH ; Account le compte dédié posé à l'enrôlement.
type Machine struct {
	ID        string
	Name      string
	Address   string
	Port      int
	Account   string
	CreatedAt time.Time
}

func (s *Store) Machines(ctx context.Context) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, address, port, account, created_at
		FROM machines ORDER BY created_at, id`)
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
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, address, port, account, created_at
		FROM machines WHERE id = ?`, id)
	machine, err := scanMachine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, ErrNotFound
	}
	return machine, err
}

// scanner : *sql.Row et *sql.Rows ont le même Scan, une seule lecture suffit.
type scanner interface {
	Scan(destinations ...any) error
}

func scanMachine(row scanner) (Machine, error) {
	var machine Machine
	var createdAt string
	err := row.Scan(&machine.ID, &machine.Name, &machine.Address,
		&machine.Port, &machine.Account, &createdAt)
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
	return machine, nil
}
