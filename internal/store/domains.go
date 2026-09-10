package store

import (
	"context"
	"fmt"
	"time"
)

// Domain est un nom publié par le proxy d'une machine. Port est celui que
// l'action a constaté dans la définition du service, jamais un port saisi.
type Domain struct {
	Name        string
	MachineID   string
	Environment string
	Service     string
	Port        int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const domainColumns = `name, machine_id, environment, service, port, created_at, updated_at`

// Domains rend les hôtes virtuels publiés, dans l'ordre où on les lit.
func (s *Store) Domains(ctx context.Context) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var domains []Domain
	for rows.Next() {
		domain, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	return domains, nil
}

// RecordDomain écrit ce que la machine porte : une publication rejouée met la
// ligne à jour, elle n'en crée pas une seconde. La date de création reste
// celle de la première publication.
func (s *Store) RecordDomain(ctx context.Context, domain Domain) error {
	moment := formatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO domains (`+domainColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET
			machine_id = excluded.machine_id,
			environment = excluded.environment,
			service = excluded.service,
			port = excluded.port,
			updated_at = excluded.updated_at`,
		domain.Name, domain.MachineID, domain.Environment, domain.Service, domain.Port,
		moment, moment)
	if err != nil {
		return fmt.Errorf("record domain: %w", err)
	}
	return nil
}

// ForgetDomain retire un nom. Un nom qu'on ne portait pas n'est pas une
// erreur : la suppression rejouée dit la même chose que la première.
func (s *Store) ForgetDomain(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM domains WHERE name = ?`, name); err != nil {
		return fmt.Errorf("forget domain: %w", err)
	}
	return nil
}

func scanDomain(row scanner) (Domain, error) {
	var domain Domain
	var createdAt, updatedAt string
	if err := row.Scan(&domain.Name, &domain.MachineID, &domain.Environment,
		&domain.Service, &domain.Port, &createdAt, &updatedAt); err != nil {
		return Domain{}, fmt.Errorf("scan domain: %w", err)
	}

	var err error
	if domain.CreatedAt, err = parseTime(createdAt); err != nil {
		return Domain{}, err
	}
	if domain.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Domain{}, err
	}
	return domain, nil
}
