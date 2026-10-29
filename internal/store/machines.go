package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Une machine se lit avec son agent courant : l'actif, sinon le dernier
// révoqué ; et ce que le jeton qui l'a enrôlé demandait.
const machineSelect = `SELECT m.id, m.name, m.hostname, m.address, m.os, m.arch, m.created_at, m.update_last,
		a.id, a.public_key, a.version, a.root_dir, a.enrolled_at, a.last_seen_at, a.revoked_at,
		a.server_key_fingerprint, a.run_as_uid, a.run_as_name, t.requested_capabilities, a.rollback_version
	FROM machines m
	LEFT JOIN agents a ON a.id = (
		SELECT id FROM agents WHERE machine_id = m.id
		ORDER BY revoked_at IS NOT NULL, enrolled_at DESC, id LIMIT 1)
	LEFT JOIN enrollment_tokens t ON t.agent_id = a.id`

// ListMachines rend les machines par nom, la machine openCloud parmi elles.
func (db *DB) ListMachines(ctx context.Context) ([]machine.Machine, error) {
	rows, err := db.sql.QueryContext(ctx, machineSelect+` ORDER BY m.name`)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	machines, err := collect(rows, scanMachine)
	if err != nil {
		return nil, err
	}
	if err := attachCapabilities(ctx, db.sql, machines); err != nil {
		return nil, err
	}
	return machines, nil
}

func (db *DB) GetMachine(ctx context.Context, id string) (machine.Machine, error) {
	row := db.sql.QueryRowContext(ctx, machineSelect+` WHERE m.id = ?`, id)
	found, err := scanMachine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return machine.Machine{}, machine.ErrNotFound
	}
	if err != nil {
		return machine.Machine{}, err
	}
	machines := []machine.Machine{found}
	if err := attachCapabilities(ctx, db.sql, machines); err != nil {
		return machine.Machine{}, err
	}
	return machines[0], nil
}

// MachineExists dit si la machine est connue : une sonde ou une tâche qui
// la vise doit le savoir avant que la clé étrangère ne la refuse en erreur
// brute.
func (db *DB) MachineExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.sql.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM machines WHERE id = ?)`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check machine: %w", err)
	}
	return exists, nil
}

// DeleteMachine relit le plan de la machine et la supprime dans la même
// transaction : tant que des services, des sondes, des tâches ou des membres
// de la page de statut la retiennent, rien ne part et le refus porte le plan.
// Un lien posé entre le plan affiché et la suppression ne passe pas ; la clé
// étrangère en restrict reste le filet. Ses agents, ce qu'elle a observé et
// mesuré partent avec elle (cascade), ses alertes restent, détachées.
func (db *DB) DeleteMachine(ctx context.Context, id string) error {
	return db.inTx(ctx, "delete machine", func(tx *sql.Tx) error {
		plan, err := machineDeletionPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := machine.RefuseDeletion(plan); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM machines WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete machine: %w", err)
		}
		return notFoundIfNoRow(result, machine.ErrNotFound)
	})
}

// TouchAgent note le signal de l'agent qui tient le flux.
func (db *DB) TouchAgent(ctx context.Context, agentID string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE agents SET last_seen_at = ? WHERE id = ?`, at.Unix(), agentID)
	if err != nil {
		return fmt.Errorf("touch agent: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// RecordServerKey note l'empreinte de la clé du serveur que l'agent dit avoir
// épinglée, à l'ouverture du flux.
func (db *DB) RecordServerKey(ctx context.Context, agentID, fingerprint string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE agents SET server_key_fingerprint = ? WHERE id = ?`, fingerprint, agentID)
	if err != nil {
		return fmt.Errorf("record server key: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// RecordRollbackVersion note la version du retour arrière que l'agent dit
// savoir faire à l'ouverture du flux ; 0 l'efface.
func (db *DB) RecordRollbackVersion(ctx context.Context, agentID string, version int) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE agents SET rollback_version = ? WHERE id = ?`, version, agentID)
	if err != nil {
		return fmt.Errorf("record rollback version: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// SetUpdateLast pose ou retire le drapeau « à mettre à jour en dernier ».
func (db *DB) SetUpdateLast(ctx context.Context, machineID string, updateLast bool) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE machines SET update_last = ? WHERE id = ?`, updateLast, machineID)
	if err != nil {
		return fmt.Errorf("set update last: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// RecordRootDir note la racine des services que l'agent dit à l'ouverture du
// flux.
func (db *DB) RecordRootDir(ctx context.Context, agentID, root string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE agents SET root_dir = ? WHERE id = ?`, root, agentID)
	if err != nil {
		return fmt.Errorf("record root dir: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// RecordSystem note le système que l'agent dit à l'ouverture du flux.
func (db *DB) RecordSystem(ctx context.Context, id, system string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE machines SET os = ? WHERE id = ?`, system, id)
	if err != nil {
		return fmt.Errorf("record system: %w", err)
	}
	return notFoundIfNoRow(result, machine.ErrNotFound)
}

// RecordConnection note ce que l'ouverture du flux dit : l'adresse de la
// machine, la version et le signe de vie de l'agent, et ses droits s'il les
// rapporte. L'enrôlement les fige, la connexion les tient à jour.
func (db *DB) RecordConnection(ctx context.Context, connection machine.Connection) error {
	return db.inTx(ctx, "record connection", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE machines SET address = ? WHERE id = ?`, connection.Address, connection.MachineID)
		if err != nil {
			return fmt.Errorf("record address: %w", err)
		}
		if err := notFoundIfNoRow(result, machine.ErrNotFound); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE agents SET version = ?, last_seen_at = ? WHERE id = ? AND machine_id = ?`,
			connection.Version, connection.At.Unix(), connection.AgentID, connection.MachineID)
		if err != nil {
			return fmt.Errorf("record agent: %w", err)
		}
		if err := notFoundIfNoRow(result, machine.ErrNotFound); err != nil {
			return err
		}
		if connection.Account != nil {
			_, err = tx.ExecContext(ctx, `UPDATE agents SET run_as_uid = ?, run_as_name = ? WHERE id = ?`,
				connection.Account.UID, connection.Account.Name, connection.AgentID)
			if err != nil {
				return fmt.Errorf("record account: %w", err)
			}
		}
		if connection.Capabilities == nil {
			return nil
		}
		return replaceCapabilities(ctx, tx, connection)
	})
}

// replaceCapabilities remplace ce que l'agent avait rapporté : c'est un
// reflet de son fichier, pas un historique.
func replaceCapabilities(ctx context.Context, tx *sql.Tx, connection machine.Connection) error {
	agentID := connection.AgentID
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_capabilities WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("clear capabilities: %w", err)
	}
	for _, capability := range connection.Capabilities {
		// Une raison pour un droit que l'agent ne rapporte pas n'a pas de ligne
		// où s'écrire : elle se perd, elle ne dit rien d'un droit coché.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_capabilities (agent_id, capability, scope, unavailable, reported_at) VALUES (?, ?, ?, ?, ?)`,
			agentID, capability.Name, strings.Join(capability.Paths, ":"), connection.Unavailable[capability.Name], connection.At.Unix())
		if err != nil {
			return fmt.Errorf("insert capability: %w", err)
		}
	}
	return nil
}

// Enroll consomme le jeton, écrit la machine et son nouvel agent dans une
// seule transaction : deux agents avec le même jeton ne passent jamais tous
// les deux. Le jeton n'est consommé que par l'UPDATE conditionnel ; l'échec
// n'est classé qu'après, pour le message. Un jeton de ré-enrôlement révoque
// l'agent actif de sa machine avant d'inscrire le nouveau. L'agent que le
// service a reconnu comme celui de la machine du serveur arrive sous
// LocalID : il reprend la ligne de la machine openCloud si elle existe.
func (db *DB) Enroll(ctx context.Context, tokenHash string, candidate machine.Machine, agent machine.Agent, now time.Time) (machine.Machine, error) {
	err := db.inTx(ctx, "enroll", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
		UPDATE enrollment_tokens SET consumed_at = ?
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
			now.Unix(), tokenHash, now.Unix())
		if err != nil {
			return fmt.Errorf("consume token: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("consume token rows: %w", err)
		}
		if affected != 1 {
			return classifyTokenFailure(ctx, tx, tokenHash, now)
		}

		var name string
		var existingID sql.NullString
		if err := tx.QueryRowContext(ctx,
			`SELECT name, machine_id FROM enrollment_tokens WHERE token_hash = ?`, tokenHash,
		).Scan(&name, &existingID); err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		candidate.Name = name
		switch {
		case existingID.Valid:
			candidate.ID = existingID.String
			err = reenroll(ctx, tx, candidate, now)
		case candidate.ID == machine.LocalID:
			err = adoptServerMachine(ctx, tx, candidate, now)
		default:
			err = insertMachine(ctx, tx, candidate)
		}
		if err != nil {
			return err
		}
		agent.MachineID = candidate.ID
		if err := insertAgent(ctx, tx, agent); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE enrollment_tokens SET agent_id = ? WHERE token_hash = ?`, agent.ID, tokenHash)
		if err != nil {
			return fmt.Errorf("link token to agent: %w", err)
		}
		return nil
	})
	if err != nil {
		return machine.Machine{}, err
	}
	return db.GetMachine(ctx, candidate.ID)
}

func insertMachine(ctx context.Context, tx *sql.Tx, m machine.Machine) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO machines (id, name, hostname, address, os, arch, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, m.Hostname, m.Address, m.OS, m.Arch, m.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return machine.ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("insert machine: %w", err)
	}
	return nil
}

// adoptServerMachine inscrit la machine openCloud sous LocalID : sa ligne
// est créée si elle manque, reprise si elle est restée sans agent (une base
// d'avant les agents, un agent révoqué). Un agent actif la garde : le
// remplacer est un ré-enrôlement, que l'opérateur demande sur sa fiche.
func adoptServerMachine(ctx context.Context, tx *sql.Tx, m machine.Machine, now time.Time) error {
	var rows, active int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*), (SELECT COUNT(*) FROM agents WHERE machine_id = ? AND revoked_at IS NULL)
		FROM machines WHERE id = ?`, m.ID, m.ID).Scan(&rows, &active)
	if err != nil {
		return fmt.Errorf("read server machine: %w", err)
	}
	switch {
	case rows == 0:
		return insertMachine(ctx, tx, m)
	case active > 0:
		return machine.ErrLocalAgentActive
	default:
		return reenroll(ctx, tx, m, now)
	}
}

// Le ré-enrôlement garde l'id, le nom et la date de création de la machine,
// rafraîchit ce que l'agent dit d'elle et révoque l'agent d'avant.
func reenroll(ctx context.Context, tx *sql.Tx, m machine.Machine, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE machines SET hostname = ?, address = ?, os = ?, arch = ?
		WHERE id = ?`,
		m.Hostname, m.Address, m.OS, m.Arch, m.ID)
	if err != nil {
		return fmt.Errorf("reenroll machine: %w", err)
	}
	if err := notFoundIfNoRow(result, machine.ErrNotFound); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE agents SET revoked_at = ? WHERE machine_id = ? AND revoked_at IS NULL`, now.Unix(), m.ID)
	if err != nil {
		return fmt.Errorf("revoke previous agent: %w", err)
	}
	return nil
}

func insertAgent(ctx context.Context, tx *sql.Tx, agent machine.Agent) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agents (id, machine_id, public_key, version, enrolled_at)
		VALUES (?, ?, ?, ?, ?)`,
		agent.ID, agent.MachineID, agent.PublicKey, agent.Version, agent.EnrolledAt.Unix())
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	return nil
}

// RevokeAgent révoque l'agent actif de la machine et annule ses jetons de
// ré-enrôlement en attente : celui qui tient une ligne d'installation encore
// valable ne remplace pas l'agent révoqué. Il rend l'agent révoqué.
func (db *DB) RevokeAgent(ctx context.Context, machineID string, now time.Time) (string, error) {
	var agentID string
	err := db.inTx(ctx, "revoke agent", func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM agents WHERE machine_id = ? AND revoked_at IS NULL`, machineID,
		).Scan(&agentID)
		if errors.Is(err, sql.ErrNoRows) {
			return machine.ErrNoAgent
		}
		if err != nil {
			return fmt.Errorf("read active agent: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agents SET revoked_at = ? WHERE id = ?`, now.Unix(), agentID); err != nil {
			return fmt.Errorf("revoke agent: %w", err)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM enrollment_tokens WHERE machine_id = ? AND consumed_at IS NULL`, machineID)
		if err != nil {
			return fmt.Errorf("cancel pending tokens: %w", err)
		}
		return nil
	})
	return agentID, err
}

func classifyTokenFailure(ctx context.Context, tx *sql.Tx, tokenHash string, now time.Time) error {
	var consumedAt sql.NullInt64
	var expiresAt int64
	err := tx.QueryRowContext(ctx,
		`SELECT consumed_at, expires_at FROM enrollment_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&consumedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return machine.ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("check token: %w", err)
	}
	if consumedAt.Valid {
		return machine.ErrTokenConsumed
	}
	if expiresAt <= now.Unix() {
		return machine.ErrTokenExpired
	}
	return machine.ErrTokenNotFound
}

func scanMachine(row scanner) (machine.Machine, error) {
	var m machine.Machine
	var createdAt int64
	var agentID, version, rootDir, serverKey, runAsName, requested sql.NullString
	var publicKey []byte
	var enrolledAt, lastSeenAt, revokedAt, runAsUID sql.NullInt64
	var rollbackVersion sql.NullInt64
	err := row.Scan(&m.ID, &m.Name, &m.Hostname, &m.Address, &m.OS, &m.Arch, &createdAt, &m.UpdateLast,
		&agentID, &publicKey, &version, &rootDir, &enrolledAt, &lastSeenAt, &revokedAt, &serverKey, &runAsUID, &runAsName, &requested, &rollbackVersion)
	if err != nil {
		return machine.Machine{}, err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	if !agentID.Valid {
		return m, nil
	}
	m.Agent = machine.Agent{
		ID: agentID.String, MachineID: m.ID, PublicKey: publicKey,
		Version: version.String, RootDir: rootDir.String, ServerKeyFingerprint: serverKey.String,
		EnrolledAt: time.Unix(enrolledAt.Int64, 0),
		LastSeenAt: timeOrZero(lastSeenAt), RevokedAt: timeOrZero(revokedAt),
		RollbackVersion: int(rollbackVersion.Int64),
	}
	if runAsUID.Valid {
		m.Agent.RunAs = &machine.Account{UID: runAsUID.Int64, Name: runAsName.String}
	}
	// Une liste illisible en base ne se montre pas : il n'y a rien à comparer.
	if requested.String != "" {
		m.Agent.Requested, _ = machine.ParseCapabilities(requested.String)
	}
	m.LastSeenAt = m.Agent.LastSeenAt
	return m, nil
}

func timeOrZero(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.Unix(value.Int64, 0)
}

// attachCapabilities pose sur chaque agent les droits qu'il a rapportés, en
// une requête pour toute la liste.
func attachCapabilities(ctx context.Context, q querier, machines []machine.Machine) error {
	agentIDs := make([]any, 0, len(machines))
	for _, m := range machines {
		if m.Agent.Exists() {
			agentIDs = append(agentIDs, m.Agent.ID)
		}
	}
	if len(agentIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(agentIDs)), ", ")
	rows, err := q.QueryContext(ctx, `
		SELECT agent_id, capability, scope, unavailable, reported_at FROM agent_capabilities
		WHERE agent_id IN (`+placeholders+`)`, agentIDs...)
	if err != nil {
		return fmt.Errorf("list capabilities: %w", err)
	}
	defer rows.Close()
	reported := map[string][]machine.Capability{}
	reportedAt := map[string]time.Time{}
	unavailable := map[string]map[string]string{}
	for rows.Next() {
		var agentID, name, scope, reason string
		var at int64
		if err := rows.Scan(&agentID, &name, &scope, &reason, &at); err != nil {
			return err
		}
		if reason != "" {
			if unavailable[agentID] == nil {
				unavailable[agentID] = map[string]string{}
			}
			unavailable[agentID][name] = reason
		}
		capability := machine.Capability{Name: name}
		if scope != "" {
			capability.Paths = strings.Split(scope, ":")
		}
		reported[agentID] = append(reported[agentID], capability)
		reportedAt[agentID] = time.Unix(at, 0)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range machines {
		agent := &machines[index].Agent
		capabilities, ok := reported[agent.ID]
		if !ok {
			continue
		}
		// Le même ordre que la liste fermée ; une ligne d'une version plus
		// récente qu'on ne connaît pas reste telle quelle.
		if normalized, err := machine.NormalizeCapabilities(capabilities); err == nil {
			capabilities = normalized
		}
		agent.Capabilities = capabilities
		agent.Unavailable = unavailable[agent.ID]
		agent.ReportedAt = reportedAt[agent.ID]
	}
	return nil
}

// MachineNames rend le nom de chaque machine, par identifiant. Le fichier
// de l'annuaire nomme les machines et ne porte jamais leurs identifiants.
func (db *DB) MachineNames(ctx context.Context) (map[string]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, name FROM machines`)
	if err != nil {
		return nil, fmt.Errorf("list machine names: %w", err)
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}
