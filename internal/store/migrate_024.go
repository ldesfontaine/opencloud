package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

// Cette étape et la migration 024_directory_links.sql qui la suit ne lisent ni
// n'écrivent rien avec les requêtes du store : elles ont les leurs, figées sur
// le schéma de la version 23, que le store courant ne suit plus. Seules les
// règles (directory.PlanLinks) sont partagées avec le code vivant.

// linkContainersToDirectory décide où vont les conteneurs qui portent un lien
// (une sonde, un membre de composant de statut, une politique de mise à jour)
// et crée, dans l'annuaire, les services qui leur manquent. Elle écrit la
// table temporaire link_migration, une ligne par fiche rattachée, archivées
// comprises, que le SQL de la migration lit pour repointer les liens. Elle
// garde aussi, dans link_migration_expect, ce que la migration ne doit pas
// perdre : verifyContainerLinks le compare après coup.
//
// Une base sans conteneur lié ne change pas : les deux tables restent vides.
func linkContainersToDirectory(ctx context.Context, tx *sql.Tx) error {
	fiches, policies, err := readFichesV23(ctx, tx)
	if err != nil {
		return err
	}
	linked, err := readLinkedIDsV23(ctx, tx)
	if err != nil {
		return err
	}
	declared, err := readDeclaredV22(ctx, tx)
	if err != nil {
		return err
	}
	plan, err := directory.PlanLinks(fiches, linked, declared, directory.NewID)
	if err != nil {
		return fmt.Errorf("plan links: %w", err)
	}
	if err := writePlanV22(ctx, tx, plan, declared, time.Now()); err != nil {
		return err
	}
	if err := createLinkTable(ctx, tx, plan); err != nil {
		return err
	}
	return recordExpectations(ctx, tx, plan, policies)
}

// readFichesV23 lit toutes les fiches de conteneur, archivées comprises, et
// la politique de chacune qui en porte une.
func readFichesV23(ctx context.Context, tx *sql.Tx) ([]container.Container, map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, machine_id, name, group_name, compose_service, image, state, ports, update_policy,
			archived_at, last_seen_at, unit_id, match_level
		FROM services`)
	if err != nil {
		return nil, nil, fmt.Errorf("read containers: %w", err)
	}
	defer rows.Close()
	var fiches []container.Container
	policies := map[string]string{}
	for rows.Next() {
		var fiche container.Container
		var state, ports, policy, matchLevel string
		var archivedAt sql.NullInt64
		var lastSeenAt int64
		var unitID sql.NullString
		err := rows.Scan(&fiche.ID, &fiche.MachineID, &fiche.Name, &fiche.Group, &fiche.ComposeService, &fiche.Image,
			&state, &ports, &policy, &archivedAt, &lastSeenAt, &unitID, &matchLevel)
		if err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal([]byte(ports), &fiche.Ports); err != nil {
			return nil, nil, fmt.Errorf("decode ports of %s: %w", fiche.ID, err)
		}
		fiche.State = container.State(state)
		fiche.ArchivedAt = timeOf(archivedAt)
		fiche.LastSeenAt = time.Unix(lastSeenAt, 0).UTC()
		if unitID.Valid {
			fiche.UnitID, fiche.MatchLevel = unitID.String, container.MatchLevel(matchLevel)
		}
		if policy != "" {
			policies[fiche.ID] = policy
		}
		fiches = append(fiches, fiche)
	}
	return fiches, policies, rows.Err()
}

// readLinkedIDsV23 nomme les fiches que quelque chose vise.
func readLinkedIDsV23(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT service_id FROM probes WHERE service_id IS NOT NULL
		UNION SELECT service_id FROM status_component_members WHERE service_id IS NOT NULL
		UNION SELECT id FROM services WHERE update_policy != ''`)
	if err != nil {
		return nil, fmt.Errorf("read linked containers: %w", err)
	}
	defer rows.Close()
	linked := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		linked[id] = true
	}
	return linked, rows.Err()
}

// readDeclaredV22 lit l'annuaire comme le rapprochement en a besoin : les
// services et leurs unités, sans le reste.
func readDeclaredV22(ctx context.Context, tx *sql.Tx) ([]directory.Service, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, machine_id, name, origin, compose_project FROM declared_services ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read declared services: %w", err)
	}
	defer rows.Close()
	var declared []directory.Service
	for rows.Next() {
		var found directory.Service
		var origin string
		if err := rows.Scan(&found.ID, &found.MachineID, &found.Name, &origin, &found.ComposeProject); err != nil {
			return nil, err
		}
		found.Origin = directory.Origin(origin)
		declared = append(declared, found)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	units, err := tx.QueryContext(ctx,
		`SELECT id, service_id, name, container_name FROM service_units ORDER BY service_id, position, id`)
	if err != nil {
		return nil, fmt.Errorf("read units: %w", err)
	}
	defer units.Close()
	position := make(map[string]int, len(declared))
	for index, found := range declared {
		position[found.ID] = index
	}
	for units.Next() {
		var unit directory.Unit
		if err := units.Scan(&unit.ID, &unit.ServiceID, &unit.Name, &unit.ContainerName); err != nil {
			return nil, err
		}
		if index, ok := position[unit.ServiceID]; ok {
			declared[index].Units = append(declared[index].Units, unit)
		}
	}
	return declared, units.Err()
}

// writePlanV22 écrit ce que le plan crée : les services, leurs unités, les
// unités ajoutées aux services de l'opérateur, et la marque « à vérifier »
// sur ceux-ci. Les colonnes sont celles de la migration 022 ; la politique de
// mise à jour n'existe pas encore, le SQL de la 024 la pose.
func writePlanV22(ctx context.Context, tx *sql.Tx, plan directory.LinkPlan, declared []directory.Service, now time.Time) error {
	for _, created := range plan.Created {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO declared_services (id, machine_id, name, title, nature, origin, compose_project,
				public_url, declared_by, needs_review, created_at, updated_at)
			VALUES (?, ?, ?, '', ?, ?, ?, '', ?, 1, ?, ?)`,
			created.ID, created.MachineID, created.Name, string(created.Nature), string(created.Origin),
			created.ComposeProject, string(created.DeclaredBy), now.Unix(), now.Unix())
		if err != nil {
			return fmt.Errorf("insert declared service %s: %w", created.Name, err)
		}
		for position, unit := range created.Units {
			if err := insertUnitV22(ctx, tx, unit, position); err != nil {
				return err
			}
		}
	}
	positions := map[string]int{}
	for _, existing := range declared {
		positions[existing.ID] = len(existing.Units)
	}
	for _, unit := range plan.Added {
		if err := insertUnitV22(ctx, tx, unit, positions[unit.ServiceID]); err != nil {
			return err
		}
		positions[unit.ServiceID]++
	}
	for _, id := range plan.Reviewed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE declared_services SET needs_review = 1, updated_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
			return fmt.Errorf("mark declared service %s for review: %w", id, err)
		}
	}
	return nil
}

func insertUnitV22(ctx context.Context, tx *sql.Tx, unit directory.Unit, position int) error {
	ports, err := portsText(unit.Ports)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO service_units (id, service_id, name, kind, image, version, digest,
			container_name, systemd_unit, ports, position)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		unit.ID, unit.ServiceID, unit.Name, string(unit.Kind), unit.Image, unit.Version, unit.Digest,
		unit.ContainerName, unit.SystemdUnit, ports, position)
	if err != nil {
		return fmt.Errorf("insert unit %s: %w", unit.Name, err)
	}
	return nil
}

// createLinkTable remplit link_migration, que le SQL de la migration lit
// puis supprime : où va chaque fiche, et le service qui porte l'unité. Une
// table temporaire, donc propre à cette connexion et à cette migration.
func createLinkTable(ctx context.Context, tx *sql.Tx, plan directory.LinkPlan) error {
	_, err := tx.ExecContext(ctx, `
		CREATE TEMP TABLE link_migration (
			container_id TEXT PRIMARY KEY,
			unit_id      TEXT NOT NULL,
			service_id   TEXT NOT NULL,
			match_level  TEXT NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create link table: %w", err)
	}
	for _, link := range plan.Links {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO temp.link_migration (container_id, unit_id, service_id, match_level) VALUES (?, ?, ?, ?)`,
			link.ContainerID, link.UnitID, link.ServiceID, string(link.Level))
		if err != nil {
			return fmt.Errorf("insert link of %s: %w", link.ContainerID, err)
		}
	}
	return nil
}

// Ce que la migration ne doit pas perdre, noté avant qu'elle ne touche à rien.
const (
	expectProbes         = "probes"
	expectProbesLinked   = "probes_linked"
	expectProbeResults   = "probe_results"
	expectProbeDays      = "probe_days"
	expectAlertsOfProbes = "alerts_of_probes"
	expectMembers        = "members"
	expectPolicyUnits    = "policy_units"
)

// recordExpectations compte, dans link_migration_expect, ce que la 024 doit
// retrouver : les sondes, leur historique, les alertes qui les visent, les
// membres de composant (moins les doublons que le SQL retire), et les unités
// qui doivent porter une politique. Le SQL de la migration reconstruit les
// tables qui portent ces données : ces comptes disent qu'il n'en a rien perdu
// en route, ce que la vérification des clés étrangères ne peut pas voir.
func recordExpectations(ctx context.Context, tx *sql.Tx, plan directory.LinkPlan, policies map[string]string) error {
	if _, err := tx.ExecContext(ctx,
		`CREATE TEMP TABLE link_migration_expect (what TEXT PRIMARY KEY, n INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create expectations: %w", err)
	}
	counts := map[string]string{
		expectProbes:         `SELECT COUNT(*) FROM probes`,
		expectProbesLinked:   `SELECT COUNT(*) FROM probes WHERE service_id IS NOT NULL`,
		expectProbeResults:   `SELECT COUNT(*) FROM probe_results`,
		expectProbeDays:      `SELECT COUNT(*) FROM probe_days`,
		expectAlertsOfProbes: `SELECT COUNT(*) FROM alerts WHERE probe_id IS NOT NULL`,
	}
	for what, query := range counts {
		var n int
		if err := tx.QueryRowContext(ctx, query).Scan(&n); err != nil {
			return fmt.Errorf("count %s: %w", what, err)
		}
		if err := setExpectation(ctx, tx, what, n); err != nil {
			return err
		}
	}
	members, err := expectedMembers(ctx, tx, plan)
	if err != nil {
		return err
	}
	if err := setExpectation(ctx, tx, expectMembers, members); err != nil {
		return err
	}
	return setExpectation(ctx, tx, expectPolicyUnits, policyUnits(plan, policies))
}

func setExpectation(ctx context.Context, tx *sql.Tx, what string, n int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO temp.link_migration_expect (what, n) VALUES (?, ?)`, what, n)
	if err != nil {
		return fmt.Errorf("record expectation %s: %w", what, err)
	}
	return nil
}

// expectedMembers rejoue en Go ce que le SQL fait des membres : ceux qui
// visent le même service dans un même composant n'en font plus qu'un, le
// premier dans l'ordre d'insertion. C'est une seconde écriture de la règle,
// voulue : si les deux divergent, la migration s'arrête.
func expectedMembers(ctx context.Context, tx *sql.Tx, plan directory.LinkPlan) (int, error) {
	serviceOf := make(map[string]string, len(plan.Links))
	for _, link := range plan.Links {
		serviceOf[link.ContainerID] = link.ServiceID
	}
	rows, err := tx.QueryContext(ctx, `SELECT component_id, service_id FROM status_component_members ORDER BY rowid`)
	if err != nil {
		return 0, fmt.Errorf("read members: %w", err)
	}
	defer rows.Close()
	type pair struct{ component, service string }
	seen := map[pair]bool{}
	kept := 0
	for rows.Next() {
		var component string
		var memberContainer sql.NullString
		if err := rows.Scan(&component, &memberContainer); err != nil {
			return 0, err
		}
		if !memberContainer.Valid {
			kept++
			continue
		}
		serviceID, ok := serviceOf[memberContainer.String]
		if !ok {
			return 0, fmt.Errorf("%w: member of component %s targets %s", directory.ErrLinkUnplanned, component, memberContainer.String)
		}
		if key := (pair{component, serviceID}); !seen[key] {
			seen[key] = true
			kept++
		}
	}
	return kept, rows.Err()
}

// policyUnits compte les unités qui doivent sortir de la migration avec une
// politique autre que watch : celles où au moins une fiche en portait une.
func policyUnits(plan directory.LinkPlan, policies map[string]string) int {
	units := map[string]bool{}
	for _, link := range plan.Links {
		if policies[link.ContainerID] != "" {
			units[link.UnitID] = true
		}
	}
	return len(units)
}

// verifyContainerLinks compare ce que la migration a laissé à ce que l'étape
// avait noté, puis range ses tables temporaires. Un écart arrête le démarrage
// avec la donnée en cause, base intacte : la transaction est annulée.
func verifyContainerLinks(ctx context.Context, tx *sql.Tx) error {
	after := map[string]string{
		expectProbes:         `SELECT COUNT(*) FROM probes`,
		expectProbesLinked:   `SELECT COUNT(*) FROM probes WHERE service_id IS NOT NULL`,
		expectProbeResults:   `SELECT COUNT(*) FROM probe_results`,
		expectProbeDays:      `SELECT COUNT(*) FROM probe_days`,
		expectAlertsOfProbes: `SELECT COUNT(*) FROM alerts WHERE probe_id IS NOT NULL`,
		expectMembers:        `SELECT COUNT(*) FROM status_component_members`,
		expectPolicyUnits:    `SELECT COUNT(*) FROM service_units WHERE update_policy != 'watch'`,
	}
	for what, query := range after {
		var expected, got int
		err := tx.QueryRowContext(ctx, `SELECT n FROM temp.link_migration_expect WHERE what = ?`, what).Scan(&expected)
		if err != nil {
			return fmt.Errorf("read expectation %s: %w", what, err)
		}
		if err := tx.QueryRowContext(ctx, query).Scan(&got); err != nil {
			return fmt.Errorf("count %s: %w", what, err)
		}
		if got != expected {
			return fmt.Errorf("%w: %s was %d before and is %d after", errMigrationLostData, what, expected, got)
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE temp.link_migration_expect`); err != nil {
		return fmt.Errorf("drop expectations: %w", err)
	}
	return nil
}

var errMigrationLostData = errors.New("migration would lose data")
