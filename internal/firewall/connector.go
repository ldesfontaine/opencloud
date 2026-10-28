package firewall

import (
	"context"
	"fmt"
)

// Connector lit un pare-feu, en lecture seule. Il n'a aucune méthode qui
// écrive : ajouter un geste d'écriture ici est un choix à discuter, pas à
// glisser. Chaque méthode rend un refus (ErrUnreachable, ErrCertificate,
// ErrUnauthorized, ErrBadResponse) quand l'API ne répond pas comme prévu.
type Connector interface {
	Version(ctx context.Context) (string, error)
	Interfaces(ctx context.Context) ([]Interface, error)
	Rules(ctx context.Context) ([]Rule, error)
	Leases(ctx context.Context) ([]Lease, error)
	Gateways(ctx context.Context) ([]Gateway, error)
}

// Factory construit le connecteur d'un pare-feu déclaré. Le câblage en pose
// une par genre (Service.SetConnector).
type Factory func(credentials Credentials) Connector

// Read fait un relevé complet. Au premier échec rien n'est rendu : un relevé
// à moitié lu remplacerait du vrai par du faux.
func Read(ctx context.Context, connector Connector) (Snapshot, error) {
	var snapshot Snapshot
	var err error
	if snapshot.Version, err = connector.Version(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("read version: %w", err)
	}
	if snapshot.Interfaces, err = connector.Interfaces(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("read interfaces: %w", err)
	}
	if snapshot.Rules, err = connector.Rules(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("read rules: %w", err)
	}
	if snapshot.Leases, err = connector.Leases(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("read leases: %w", err)
	}
	if snapshot.Gateways, err = connector.Gateways(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("read gateways: %w", err)
	}
	return snapshot, nil
}
