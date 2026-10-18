package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

const containerColumns = `id, machine_id, kind, name, group_name, container_id, image, image_id, state, exit_code,
	health, restart_count, ports, network_mode, privileged, depends_on, created_at, started_at, finished_at, first_seen_at, last_seen_at, archived_at,
	compose_service, compose_dir, compose_file, oom_killed, killed, label_service, label_unit, label_invalid`

// Le rattachement à l'annuaire se lit avec la fiche. Il ne s'écrit pas
// comme le reste, l'unité est vérifiée à l'écriture : voir upsertContainer.
// La politique de mise à jour et le service déclaré sont ceux de l'unité de
// la fiche, lus en sous-requêtes corrélées plutôt qu'en jointure : les deux
// tables partagent id, name, image et ports. Sans unité, la fiche suit.
const containerReadColumns = containerColumns + `, unit_id, match_level,
	COALESCE((SELECT u.update_policy FROM service_units u WHERE u.id = containers.unit_id), 'watch'),
	COALESCE((SELECT u.service_id FROM service_units u WHERE u.id = containers.unit_id), '')`

// ListContainers rend les fiches d'une machine, ou de toutes si machineID
// est vide, archivées comprises, par groupe puis par nom.
func (db *DB) ListContainers(ctx context.Context, machineID string) ([]container.Container, error) {
	query := `SELECT ` + containerReadColumns + ` FROM containers`
	var args []any
	if machineID != "" {
		query += ` WHERE machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY machine_id, group_name, name, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer rows.Close()
	containers := []container.Container{}
	for rows.Next() {
		item, err := scanContainer(rows)
		if err != nil {
			return nil, err
		}
		containers = append(containers, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if machineID == "" {
		return containers, db.attachNetworks(ctx, ``, nil, containers)
	}
	return containers, db.attachNetworks(ctx, `WHERE container_id IN (SELECT id FROM containers WHERE machine_id = ?)`, []any{machineID}, containers)
}

func (db *DB) GetContainer(ctx context.Context, id string) (container.Container, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+containerReadColumns+` FROM containers WHERE id = ?`, id)
	item, err := scanContainer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return container.Container{}, container.ErrNotFound
	}
	if err != nil {
		return container.Container{}, err
	}
	containers := []container.Container{item}
	if err := db.attachNetworks(ctx, `WHERE container_id = ?`, []any{id}, containers); err != nil {
		return container.Container{}, err
	}
	return containers[0], nil
}

// attachNetworks lit en une requête les appartenances des fiches que la
// clause désigne et les pose sur chacune, par nom de réseau.
func (db *DB) attachNetworks(ctx context.Context, where string, args []any, containers []container.Container) error {
	if len(containers) == 0 {
		return nil
	}
	query := `SELECT container_id, network_id, name, ip, aliases FROM container_networks ` + where + ` ORDER BY container_id, name` // #nosec G202 -- la clause est une constante du package, les valeurs sont liées.
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("list container networks: %w", err)
	}
	defer rows.Close()
	byContainer := map[string][]container.Attachment{}
	for rows.Next() {
		var containerID, aliases string
		var attachment container.Attachment
		if err := rows.Scan(&containerID, &attachment.NetworkID, &attachment.Name, &attachment.IP, &aliases); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(aliases), &attachment.Aliases); err != nil {
			return fmt.Errorf("decode aliases of %s: %w", containerID, err)
		}
		byContainer[containerID] = append(byContainer[containerID], attachment)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range containers {
		containers[i].Networks = byContainer[containers[i].ID]
	}
	return nil
}

// ApplyChanges écrit un rapport entier dans une transaction : fiches,
// archivage des absentes, transitions, échantillons, état du Docker.
func (db *DB) ApplyChanges(ctx context.Context, changes container.Changes) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin apply containers: %w", err)
	}
	defer tx.Rollback()
	for _, item := range changes.Containers {
		if err := upsertContainer(ctx, tx, item); err != nil {
			return err
		}
	}
	if changes.ArchiveMissing {
		if err := archiveMissing(ctx, tx, changes.MachineID, changes.Keep, changes.Now); err != nil {
			return err
		}
	}
	for _, transition := range changes.Transitions {
		if err := insertTransition(ctx, tx, transition); err != nil {
			return err
		}
	}
	for _, sample := range changes.Samples {
		if err := insertSample(ctx, tx, sample); err != nil {
			return err
		}
	}
	if changes.Engine != nil {
		if err := saveEngine(ctx, tx, *changes.Engine); err != nil {
			return err
		}
	}
	if changes.ReplaceNetworks {
		if err := replaceMachineNetworks(ctx, tx, changes.MachineID, changes.Networks); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apply containers: %w", err)
	}
	return nil
}

func upsertContainer(ctx context.Context, tx *sql.Tx, item container.Container) error {
	ports, err := encodeList(item.Ports)
	if err != nil {
		return fmt.Errorf("encode ports: %w", err)
	}
	dependsOn, err := encodeList(item.DependsOn)
	if err != nil {
		return fmt.Errorf("encode dependencies: %w", err)
	}
	// L'unité se lit en sous-requête : si l'annuaire l'a supprimée depuis
	// que le rapprochement l'a nommée, la fiche s'écrit « non déclarée »
	// plutôt que de faire échouer la clé étrangère et perdre le rapport.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO containers (`+containerColumns+`, unit_id, match_level)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT id FROM service_units WHERE id = ?),
			CASE WHEN EXISTS (SELECT 1 FROM service_units WHERE id = ?) THEN ? ELSE '' END)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, group_name = excluded.group_name, image = excluded.image,
			image_id = excluded.image_id, state = excluded.state, exit_code = excluded.exit_code, oom_killed = excluded.oom_killed, killed = excluded.killed,
			health = excluded.health, restart_count = excluded.restart_count, ports = excluded.ports,
			network_mode = excluded.network_mode, privileged = excluded.privileged, depends_on = excluded.depends_on,
			created_at = excluded.created_at, started_at = excluded.started_at, finished_at = excluded.finished_at,
			last_seen_at = excluded.last_seen_at, archived_at = excluded.archived_at,
			compose_service = excluded.compose_service, compose_dir = excluded.compose_dir, compose_file = excluded.compose_file,
			label_service = excluded.label_service, label_unit = excluded.label_unit, label_invalid = excluded.label_invalid,
			unit_id = excluded.unit_id, match_level = excluded.match_level`,
		item.ID, item.MachineID, string(item.Kind), item.Name, item.Group, item.ContainerID, item.Image, item.ImageID,
		string(item.State), item.ExitCode, string(item.Health), item.RestartCount, ports,
		item.NetworkMode, boolToInt(item.Privileged), dependsOn,
		item.CreatedAt.Unix(), nullableTime(item.StartedAt), nullableTime(item.FinishedAt),
		item.FirstSeenAt.Unix(), item.LastSeenAt.Unix(), nullableTime(item.ArchivedAt),
		item.ComposeService, item.ComposeDir, item.ComposeFile, boolToInt(item.OOMKilled), boolToInt(item.Killed),
		item.Labels.Service, item.Labels.Unit, boolToInt(item.Labels.Invalid),
		item.UnitID, item.UnitID, string(item.MatchLevel))
	if err != nil {
		return fmt.Errorf("upsert container %s: %w", item.ID, err)
	}
	return replaceContainerNetworks(ctx, tx, item)
}

// SetContainerUnits écrit le rattachement des fiches nommées, dans une
// transaction. Une unité qui n'existe plus s'écrit comme « non déclaré » :
// l'annuaire a pu la supprimer entre le rapprochement et l'écriture.
func (db *DB) SetContainerUnits(ctx context.Context, assignments []container.UnitAssignment) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set container units: %w", err)
	}
	defer tx.Rollback()
	for _, assignment := range assignments {
		_, err := tx.ExecContext(ctx, `
			UPDATE containers SET
				unit_id = (SELECT id FROM service_units WHERE id = ?),
				match_level = CASE WHEN EXISTS (SELECT 1 FROM service_units WHERE id = ?) THEN ? ELSE '' END
			WHERE id = ?`,
			assignment.UnitID, assignment.UnitID, string(assignment.Level), assignment.ContainerID)
		if err != nil {
			return fmt.Errorf("set unit of container %s: %w", assignment.ContainerID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit set container units: %w", err)
	}
	return nil
}

// Une liste nulle s'écrit « [] » : la colonne ne connaît pas null.
func encodeList[T any](list []T) (string, error) {
	if list == nil {
		return "[]", nil
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// replaceContainerNetworks réécrit les appartenances d'une fiche : ce que le
// conteneur dit à l'instant fait foi.
func replaceContainerNetworks(ctx context.Context, tx *sql.Tx, item container.Container) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM container_networks WHERE container_id = ?`, item.ID); err != nil {
		return fmt.Errorf("clear container networks %s: %w", item.ID, err)
	}
	for _, attachment := range item.Networks {
		aliases, err := encodeList(attachment.Aliases)
		if err != nil {
			return fmt.Errorf("encode aliases: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO container_networks (container_id, network_id, name, ip, aliases) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (container_id, network_id) DO UPDATE SET name = excluded.name, ip = excluded.ip, aliases = excluded.aliases`,
			item.ID, attachment.NetworkID, attachment.Name, attachment.IP, aliases)
		if err != nil {
			return fmt.Errorf("insert container network %s: %w", item.ID, err)
		}
	}
	return nil
}

// replaceMachineNetworks remplace la liste des réseaux de la machine.
func replaceMachineNetworks(ctx context.Context, tx *sql.Tx, machineID string, networks []container.Network) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM machine_networks WHERE machine_id = ?`, machineID); err != nil {
		return fmt.Errorf("clear machine networks: %w", err)
	}
	for _, network := range networks {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO machine_networks (machine_id, network_id, name, driver, internal, group_name, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (machine_id, network_id) DO UPDATE SET name = excluded.name, driver = excluded.driver,
				internal = excluded.internal, group_name = excluded.group_name, seen_at = excluded.seen_at`,
			machineID, network.NetworkID, network.Name, network.Driver, boolToInt(network.Internal), network.Group, network.SeenAt.Unix())
		if err != nil {
			return fmt.Errorf("insert machine network %s: %w", network.Name, err)
		}
	}
	return nil
}

// ListNetworks rend les réseaux d'une machine, par nom.
func (db *DB) ListNetworks(ctx context.Context, machineID string) ([]container.Network, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT machine_id, network_id, name, driver, internal, group_name, seen_at FROM machine_networks
		WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list machine networks: %w", err)
	}
	defer rows.Close()
	networks := []container.Network{}
	for rows.Next() {
		var network container.Network
		var internal int
		var seenAt int64
		if err := rows.Scan(&network.MachineID, &network.NetworkID, &network.Name, &network.Driver, &internal, &network.Group, &seenAt); err != nil {
			return nil, err
		}
		network.Internal = internal != 0
		network.SeenAt = time.Unix(seenAt, 0).UTC()
		networks = append(networks, network)
	}
	return networks, rows.Err()
}

// archiveMissing archive les fiches vivantes de la machine que
// l'inventaire ne nomme plus.
func archiveMissing(ctx context.Context, tx *sql.Tx, machineID string, keep []string, now time.Time) error {
	args := []any{now.Unix(), machineID}
	query := `UPDATE containers SET archived_at = ? WHERE machine_id = ? AND archived_at IS NULL`
	if len(keep) > 0 {
		query += ` AND id NOT IN (?` + strings.Repeat(", ?", len(keep)-1) + `)` // #nosec G202 -- des « ? » répétés, les valeurs sont liées.
		for _, id := range keep {
			args = append(args, id)
		}
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("archive missing containers: %w", err)
	}
	return nil
}

func insertTransition(ctx context.Context, tx *sql.Tx, t container.Transition) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO container_events (container_id, at, action, previous_state, new_state, previous_health, new_health, exit_code, oom_killed, killed, replayed, snippet)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ContainerID, t.At.Unix(), t.Action, string(t.PreviousState), string(t.NewState),
		string(t.PreviousHealth), string(t.NewHealth), nullableInt(t.ExitCode), boolToInt(t.OOMKilled), boolToInt(t.Killed), boolToInt(t.Replayed), t.Snippet)
	if err != nil {
		return fmt.Errorf("insert transition: %w", err)
	}
	return nil
}

// Un échantillon déjà là, rejoué après une coupure, est ignoré.
func insertSample(ctx context.Context, tx *sql.Tx, sample container.Sample) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO container_samples (container_id, sampled_at, cpu_percent, mem_used, mem_limit)
		VALUES (?, ?, ?, ?, ?)`,
		sample.ContainerID, sample.SampledAt.Unix(), sample.CPUPercent, sample.MemUsed, sample.MemLimit)
	if err != nil {
		return fmt.Errorf("insert container sample: %w", err)
	}
	return nil
}

func saveEngine(ctx context.Context, tx *sql.Tx, engine container.Engine) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO machine_engines (machine_id, present, reason, version, api_version, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (machine_id) DO UPDATE SET present = excluded.present, reason = excluded.reason,
			version = excluded.version, api_version = excluded.api_version, checked_at = excluded.checked_at`,
		engine.MachineID, boolToInt(engine.Present), string(engine.Reason), engine.Version, engine.APIVersion, engine.CheckedAt.Unix())
	if err != nil {
		return fmt.Errorf("save engine: %w", err)
	}
	return nil
}

// ListTransitions rend les plus récentes d'abord.
func (db *DB) ListTransitions(ctx context.Context, containerID string, limit int) ([]container.Transition, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, container_id, at, action, previous_state, new_state, previous_health, new_health, exit_code, oom_killed, killed, replayed, snippet
		FROM container_events WHERE container_id = ? ORDER BY at DESC, id DESC LIMIT ?`, containerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list transitions: %w", err)
	}
	defer rows.Close()
	transitions := []container.Transition{}
	for rows.Next() {
		var t container.Transition
		var at int64
		var exitCode sql.NullInt64
		var oomKilled, killed, replayed int
		if err := rows.Scan(&t.ID, &t.ContainerID, &at, &t.Action, &t.PreviousState, &t.NewState, &t.PreviousHealth, &t.NewHealth, &exitCode, &oomKilled, &killed, &replayed, &t.Snippet); err != nil {
			return nil, err
		}
		t.At = time.Unix(at, 0).UTC()
		t.ExitCode = intPointer(exitCode)
		t.OOMKilled = oomKilled != 0
		t.Killed = killed != 0
		t.Replayed = replayed != 0
		transitions = append(transitions, t)
	}
	return transitions, rows.Err()
}

// LatestContainerSamples rend le dernier échantillon de chaque conteneur.
func (db *DB) LatestContainerSamples(ctx context.Context) ([]container.Sample, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT container_id, sampled_at, cpu_percent, mem_used, mem_limit FROM container_samples
		WHERE (container_id, sampled_at) IN (SELECT container_id, MAX(sampled_at) FROM container_samples GROUP BY container_id)
		ORDER BY container_id`)
	if err != nil {
		return nil, fmt.Errorf("list latest container samples: %w", err)
	}
	defer rows.Close()
	samples := []container.Sample{}
	for rows.Next() {
		var sample container.Sample
		var sampledAt int64
		if err := rows.Scan(&sample.ContainerID, &sampledAt, &sample.CPUPercent, &sample.MemUsed, &sample.MemLimit); err != nil {
			return nil, err
		}
		sample.SampledAt = time.Unix(sampledAt, 0).UTC()
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

func (db *DB) ListEngines(ctx context.Context) ([]container.Engine, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT machine_id, present, reason, version, api_version, checked_at FROM machine_engines ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list engines: %w", err)
	}
	defer rows.Close()
	engines := []container.Engine{}
	for rows.Next() {
		var engine container.Engine
		var present int
		var checkedAt int64
		if err := rows.Scan(&engine.MachineID, &present, &engine.Reason, &engine.Version, &engine.APIVersion, &checkedAt); err != nil {
			return nil, err
		}
		engine.Present = present != 0
		engine.CheckedAt = time.Unix(checkedAt, 0).UTC()
		engines = append(engines, engine)
	}
	return engines, rows.Err()
}

// PurgeContainers efface les transitions trop vieilles, les fiches archivées
// depuis trop longtemps (leurs transitions et échantillons partent avec),
// et le brut des mesures.
func (db *DB) PurgeContainers(ctx context.Context, transitionsBefore, archivedBefore, samplesBefore time.Time) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin purge containers: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM container_events WHERE at < ?`, transitionsBefore.Unix()); err != nil {
		return fmt.Errorf("purge transitions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM containers WHERE archived_at IS NOT NULL AND archived_at < ?`, archivedBefore.Unix()); err != nil {
		return fmt.Errorf("purge archived containers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM container_samples WHERE sampled_at < ?`, samplesBefore.Unix()); err != nil {
		return fmt.Errorf("purge container samples: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit purge containers: %w", err)
	}
	return nil
}

func scanContainer(row scanner) (container.Container, error) {
	var item container.Container
	var kind, state, health, ports, dependsOn, matchLevel, policy string
	var unitID sql.NullString
	var privileged, oomKilled, killed, labelInvalid int
	var createdAt, firstSeen, lastSeen int64
	var startedAt, finishedAt, archivedAt sql.NullInt64
	err := row.Scan(&item.ID, &item.MachineID, &kind, &item.Name, &item.Group, &item.ContainerID, &item.Image, &item.ImageID,
		&state, &item.ExitCode, &health, &item.RestartCount, &ports, &item.NetworkMode, &privileged, &dependsOn,
		&createdAt, &startedAt, &finishedAt, &firstSeen, &lastSeen, &archivedAt,
		&item.ComposeService, &item.ComposeDir, &item.ComposeFile, &oomKilled, &killed, &item.Labels.Service, &item.Labels.Unit, &labelInvalid,
		&unitID, &matchLevel, &policy, &item.DeclaredServiceID)
	if err != nil {
		return container.Container{}, err
	}
	// Sans unité, le niveau n'a pas de sens : une unité supprimée met
	// unit_id à NULL sans toucher à match_level.
	if unitID.Valid {
		item.UnitID, item.MatchLevel = unitID.String, container.MatchLevel(matchLevel)
	}
	item.OOMKilled = oomKilled != 0
	item.Killed = killed != 0
	item.Labels.Invalid = labelInvalid != 0
	item.Kind, item.State, item.Health = container.Kind(kind), container.State(state), container.Health(health)
	item.UpdatePolicy = container.UpdatePolicy(policy)
	item.Privileged = privileged != 0
	if err := json.Unmarshal([]byte(ports), &item.Ports); err != nil {
		return container.Container{}, fmt.Errorf("decode ports of %s: %w", item.ID, err)
	}
	if err := json.Unmarshal([]byte(dependsOn), &item.DependsOn); err != nil {
		return container.Container{}, fmt.Errorf("decode dependencies of %s: %w", item.ID, err)
	}
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
	item.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	item.StartedAt, item.FinishedAt, item.ArchivedAt = timeOf(startedAt).UTC(), timeOf(finishedAt).UTC(), timeOf(archivedAt).UTC()
	return item, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
