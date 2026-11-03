package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/restic"
)

// allServices : le filtre vide, tous les services déclarés.
const allServices = ""

// ResticServiceDocker rend ce que l'inventaire Docker dit d'un service : les
// conteneurs (non archivés) rattachés à ses unités sur sa machine, et les
// volumes de cette machine. Le composant en dérive ce qu'il sauvegarde.
func (db *DB) ResticServiceDocker(ctx context.Context, serviceID string) (restic.ServiceDocker, error) {
	byService, err := db.readServiceDocker(ctx, serviceID)
	if err != nil {
		return restic.ServiceDocker{}, err
	}
	return byService[serviceID], nil
}

// ListResticServiceDocker fait de même pour chaque service, par identifiant.
func (db *DB) ListResticServiceDocker(ctx context.Context) (map[string]restic.ServiceDocker, error) {
	return db.readServiceDocker(ctx, allServices)
}

func (db *DB) readServiceDocker(ctx context.Context, serviceID string) (map[string]restic.ServiceDocker, error) {
	byService := map[string]restic.ServiceDocker{}
	containers, err := db.serviceContainers(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, entry := range containers {
		docker := byService[entry.serviceID]
		docker.Containers = append(docker.Containers, entry.container)
		byService[entry.serviceID] = docker
	}
	volumes, err := db.serviceMachineVolumes(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, entry := range volumes {
		docker := byService[entry.serviceID]
		docker.Volumes = append(docker.Volumes, entry.volume)
		byService[entry.serviceID] = docker
	}
	return byService, nil
}

type serviceContainer struct {
	serviceID string
	container restic.DockerContainer
}

// serviceContainers : un conteneur est au service par son unité, et sur la
// machine du service.
func (db *DB) serviceContainers(ctx context.Context, serviceID string) ([]serviceContainer, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT u.service_id, c.name, c.image
		FROM containers c
		JOIN service_units u ON u.id = c.unit_id
		JOIN declared_services s ON s.id = u.service_id AND s.machine_id = c.machine_id
		WHERE c.archived_at IS NULL AND (? = '' OR u.service_id = ?)
		ORDER BY u.service_id, c.name`, serviceID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service containers: %w", err)
	}
	return collect(rows, func(row scanner) (serviceContainer, error) {
		var entry serviceContainer
		err := row.Scan(&entry.serviceID, &entry.container.Name, &entry.container.Image)
		return entry, err
	})
}

type serviceVolume struct {
	serviceID string
	volume    restic.DockerVolume
}

// serviceMachineVolumes : les volumes de la machine de chaque service ; le
// composant garde ceux qu'un conteneur du service monte.
func (db *DB) serviceMachineVolumes(ctx context.Context, serviceID string) ([]serviceVolume, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT s.id, v.name, v.used_by
		FROM machine_volumes v JOIN declared_services s ON s.machine_id = v.machine_id
		WHERE ? = '' OR s.id = ?
		ORDER BY s.id, v.name`, serviceID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list service volumes: %w", err)
	}
	return collect(rows, func(row scanner) (serviceVolume, error) {
		var entry serviceVolume
		var usedBy string
		if err := row.Scan(&entry.serviceID, &entry.volume.Name, &usedBy); err != nil {
			return serviceVolume{}, err
		}
		if err := json.Unmarshal([]byte(usedBy), &entry.volume.UsedBy); err != nil {
			return serviceVolume{}, fmt.Errorf("decode volume users: %w", err)
		}
		return entry, nil
	})
}
