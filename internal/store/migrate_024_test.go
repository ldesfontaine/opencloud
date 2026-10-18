package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/migrations"
)

// Les tests de la migration 024 partent d'une base au schéma de la 023, y
// sèment de l'existant en SQL brut (le store courant ne sait plus écrire
// ce schéma), puis laissent Open la mettre au niveau.

// openAtVersion crée une base dans un répertoire neuf et la laisse à la
// version donnée. Le *DB rendu est celui qu'Open rendrait ensuite : db.migrate
// finit le travail, sur la même base.
func openAtVersion(t *testing.T, version int) *DB {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	handle, err := openHandle(root)
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: handle}
	t.Cleanup(func() { db.Close() })
	db.SetSealer(seal.Generate())
	if err := db.migrateTo(context.Background(), version); err != nil {
		t.Fatalf("migrer jusqu'à la version %d: %v", version, err)
	}
	return db
}

func exec(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.sql.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", strings.Join(strings.Fields(query), " "), err)
	}
}

func count(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.sql.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", strings.Join(strings.Fields(query), " "), err)
	}
	return n
}

func text(t *testing.T, db *DB, query string, args ...any) string {
	t.Helper()
	var got sql.NullString
	if err := db.sql.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", strings.Join(strings.Fields(query), " "), err)
	}
	return got.String
}

func migrateToTheEnd(t *testing.T, db *DB) {
	t.Helper()
	if err := db.migrate(context.Background()); err != nil {
		t.Fatalf("migrer: %v", err)
	}
}

const (
	m1 = "11111111-2222-4333-8444-555555555555"
	m2 = "22222222-2222-4333-8444-555555555555"
)

func seedMachines(t *testing.T, db *DB) {
	t.Helper()
	exec(t, db, `INSERT INTO machines (id, name, kind, enrolled_at, created_at) VALUES ('local', 'opencloud', 'local', 1, 1)`)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-1', 'remote', zeroblob(32), 1, 1)`, m1)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-2', 'remote', zeroblob(32), 1, 1)`, m2)
}

// fiche est une ligne de la table services au schéma de la 023.
type fiche struct {
	id, machine, name, group, composeService, image string
	policy                                          string // '', pinned, excluded
	lastSeen                                        int64
	archived                                        bool
	unitID                                          string
	level                                           string
	state                                           string // running par défaut
}

func (f fiche) with(change func(*fiche)) fiche {
	change(&f)
	return f
}

func seedFiche(t *testing.T, db *DB, f fiche) {
	t.Helper()
	if f.image == "" {
		f.image = "nginx:1.27"
	}
	if f.lastSeen == 0 {
		f.lastSeen = 1_800_000_000
	}
	var archivedAt any
	state := "running"
	if f.state != "" {
		state = f.state
	}
	if f.archived {
		archivedAt, state = f.lastSeen, "exited"
	}
	var unit any
	if f.unitID != "" {
		unit = f.unitID
	}
	exec(t, db, `
		INSERT INTO services (id, machine_id, kind, name, group_name, container_id, image, state, ports,
			created_at, first_seen_at, last_seen_at, archived_at, compose_service, update_policy, unit_id, match_level)
		VALUES (?, ?, 'container', ?, ?, ?, ?, ?, '[]', 1, 1, ?, ?, ?, ?, ?, ?)`,
		f.id, f.machine, f.name, f.group, f.id, f.image, state, f.lastSeen, archivedAt, f.composeService, f.policy, unit, f.level)
}

// composeFiche : un conteneur d'un projet Compose, nommé comme Compose le nomme.
func composeFiche(id, machine, project, composeService string) fiche {
	return fiche{id: id, machine: machine, name: project + "-" + composeService + "-1", group: project, composeService: composeService, image: composeService + ":1"}
}

func loneFiche(id, machine, name string) fiche {
	return fiche{id: id, machine: machine, name: name, image: "traefik/whoami:v1"}
}

func seedProbe(t *testing.T, db *DB, id, machine, serviceID string) {
	t.Helper()
	var service any
	if serviceID != "" {
		service = serviceID
	}
	exec(t, db, `
		INSERT INTO probes (id, name, kind, target, machine_id, service_id, status, interval_seconds, timeout_seconds,
			failure_threshold, recovery_threshold, method, expected_status, expected_body, follow_redirects,
			consecutive_failures, consecutive_successes, last_checked_at, last_duration_ms, last_code, last_reason,
			cert_subject, cert_issuer, cert_not_before, cert_not_after, cert_fingerprint, created_at,
			tls, cert_chain_valid, cert_hostname_match, cert_ocsp, revised_at)
		VALUES (?, ?, 'http', 'https://cloud.exemple.fr/', ?, ?, 'up', 120, 8, 4, 2, 'GET', '200-299', 'ok', 1,
			1, 7, 1700000500, 42, 200, 'ok',
			'cloud.exemple.fr', 'R3', 1690000000, 1790000000, 'abcdef', 1700000000,
			1, 1, 1, 'good', 1700000100)`,
		id, "sonde "+id, machine, service)
}

func seedProbeHistory(t *testing.T, db *DB, probeID string, results, days int) {
	t.Helper()
	for index := range results {
		exec(t, db, `INSERT INTO probe_results (probe_id, checked_at, outcome, duration_ms, code, reason) VALUES (?, ?, 'up', 30, 200, '')`,
			probeID, 1_700_000_000+index*120)
	}
	for index := range days {
		exec(t, db, `INSERT INTO probe_days (probe_id, day, total, success, degraded, duration_ms) VALUES (?, ?, 720, 718, 1, 21000)`,
			probeID, 19_000+index)
	}
}

func seedAlert(t *testing.T, db *DB, kind, objectKind, objectID, probeID, serviceID string) {
	t.Helper()
	var probe, service any
	if probeID != "" {
		probe = probeID
	}
	if serviceID != "" {
		service = serviceID
	}
	exec(t, db, `
		INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, probe_id, service_id, opened_at, updated_at)
		VALUES (?, 'danger', 'open', ?, ?, 'objet', ?, ?, 1, 1)`, kind, objectKind, objectID, probe, service)
}

func seedComponent(t *testing.T, db *DB, id string) {
	t.Helper()
	exec(t, db, `INSERT INTO status_components (id, name, position, created_at) VALUES (?, ?, 0, 1)`, id, "composant "+id)
}

func seedMember(t *testing.T, db *DB, component, column, target string) {
	t.Helper()
	exec(t, db, fmt.Sprintf(`INSERT INTO status_component_members (component_id, %s) VALUES (?, ?)`, column), component, target)
}

func seedDeclaredCompose(t *testing.T, db *DB, id, machine, project string, reviewed bool, units ...string) {
	t.Helper()
	exec(t, db, `
		INSERT INTO declared_services (id, machine_id, name, nature, origin, compose_project, declared_by, needs_review, created_at, updated_at)
		VALUES (?, ?, ?, 'application', 'compose', ?, 'operator', ?, 1, 1)`, id, machine, project, project, boolInt(!reviewed))
	for position, name := range units {
		exec(t, db, `
			INSERT INTO service_units (id, service_id, name, kind, image, version, position)
			VALUES (?, ?, ?, 'container', ?, '1', ?)`, "unit_"+project+"_"+name, id, name, name, position)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// serviceOf rend l'unique service déclaré de la machine et du nom, ou arrête.
func declaredNamed(t *testing.T, db *DB, machine, name string) directory.Service {
	t.Helper()
	id := text(t, db, `SELECT id FROM declared_services WHERE machine_id = ? AND name = ?`, machine, name)
	if id == "" {
		t.Fatalf("aucun service déclaré %q sur %s", name, machine)
	}
	found, err := db.GetDeclaredService(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func unitNamed(t *testing.T, found directory.Service, name string) directory.Unit {
	t.Helper()
	for _, unit := range found.Units {
		if unit.Name == name {
			return unit
		}
	}
	t.Fatalf("pas d'unité %q dans %+v", name, found.Units)
	return directory.Unit{}
}

func ficheUnit(t *testing.T, db *DB, id string) string {
	t.Helper()
	return text(t, db, `SELECT unit_id FROM services WHERE id = ?`, id)
}

// assertRematchStable : la reprise ne doit laisser au rapprochement du
// prochain démarrage rien à déplacer. Elle rejoue la règle sur les fiches
// vivantes et compare à ce que la migration a écrit.
func assertRematchStable(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	declared, err := db.ListDeclaredServices(ctx, directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	fiches, err := db.ListServices(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	matches := directory.Match(declared, fiches)
	for _, found := range fiches {
		if found.IsArchived() {
			continue
		}
		want := matches[found.ID]
		if found.UnitID != want.UnitID || (want.UnitID != "" && found.MatchLevel != want.Level) {
			t.Errorf("la fiche %s est à l'unité %q (%s) ; le rapprochement la mettrait à %q (%s)",
				found.ID, found.UnitID, found.MatchLevel, want.UnitID, want.Level)
		}
	}
}

// Une sonde sur un conteneur Compose : la migration crée le service du projet,
// avec une unité par service Compose, repointe la sonde dessus et rattache les
// conteneurs. Le reste de la sonde ne bouge pas.
func TestMigration024_ProbeOnComposeContainerFollowsTheNewService(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, composeFiche("m1:app", m1, "nextcloud", "app").with(func(f *fiche) { f.image = "nextcloud:29.0.4" }))
	seedFiche(t, db, composeFiche("m1:db", m1, "nextcloud", "db").with(func(f *fiche) { f.image = "postgres:16.4" }))
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "m1:app")
	before := text(t, db, `SELECT cert_fingerprint || '|' || last_code || '|' || consecutive_successes || '|' || revised_at FROM probes WHERE id = 'probe1'`)

	migrateToTheEnd(t, db)

	created := declaredNamed(t, db, m1, "nextcloud")
	if created.DeclaredBy != directory.ByMigration || !created.NeedsReview || created.Origin != directory.OriginCompose || created.ComposeProject != "nextcloud" {
		t.Fatalf("service = %+v", created)
	}
	if len(created.Units) != 2 || unitNamed(t, created, "app").Image != "nextcloud" || unitNamed(t, created, "app").Version != "29.0.4" || unitNamed(t, created, "db").Image != "postgres" {
		t.Fatalf("unités = %+v", created.Units)
	}
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != created.ID {
		t.Fatalf("la sonde vise %q, want %q", got, created.ID)
	}
	if after := text(t, db, `SELECT cert_fingerprint || '|' || last_code || '|' || consecutive_successes || '|' || revised_at FROM probes WHERE id = 'probe1'`); after != before {
		t.Fatalf("le reste de la sonde a bougé: %q -> %q", before, after)
	}
	if ficheUnit(t, db, "m1:app") != unitNamed(t, created, "app").ID || ficheUnit(t, db, "m1:db") != unitNamed(t, created, "db").ID {
		t.Fatal("les conteneurs du projet doivent être rattachés à leur unité")
	}
	if got := text(t, db, `SELECT match_level FROM services WHERE id = 'm1:app'`); got != string(service.MatchCompose) {
		t.Fatalf("niveau = %q", got)
	}
	// Le conteneur seul, que rien ne visait, n'est pas repris.
	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 1 {
		t.Fatalf("%d services déclarés, un seul attendu", got)
	}
	if ficheUnit(t, db, "m1:whoami") != "" {
		t.Fatal("un conteneur sans lien reste non déclaré")
	}
	assertRematchStable(t, db)
}

// La fiche archivée porte la sonde et la politique ; la vivante qui l'a
// remplacée n'a rien. Un seul service, une seule unité : la sonde est repointée,
// la politique est sur l'unité, les deux fiches y sont rattachées. C'est ce
// qui rend à un conteneur déjà recréé ce que la recréation lui avait fait perdre.
func TestMigration024_RecreatedContainerArchivedLinkGoesToTheUnit(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, composeFiche("m1:old", m1, "shop", "web").with(func(f *fiche) {
		f.archived, f.policy, f.lastSeen = true, "pinned", 1_700_000_000
	}))
	seedFiche(t, db, composeFiche("m1:new", m1, "shop", "web").with(func(f *fiche) { f.lastSeen = 1_800_000_000 }))
	seedProbe(t, db, "probe1", m1, "m1:old")

	migrateToTheEnd(t, db)

	created := declaredNamed(t, db, m1, "shop")
	if len(created.Units) != 1 {
		t.Fatalf("unités = %+v", created.Units)
	}
	unit := created.Units[0]
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != created.ID {
		t.Fatalf("la sonde vise %q", got)
	}
	if unit.UpdatePolicy != service.PolicyHold {
		t.Fatalf("politique de l'unité = %q, want hold", unit.UpdatePolicy)
	}
	if ficheUnit(t, db, "m1:old") != unit.ID || ficheUnit(t, db, "m1:new") != unit.ID {
		t.Fatal("l'archivée et la vivante doivent être à la même unité")
	}
	// La fiche vivante, sans politique propre, lit celle de l'unité.
	fresh, err := db.GetService(context.Background(), "m1:new")
	if err != nil || fresh.UpdatePolicy != service.PolicyHold || fresh.DeclaredServiceID != created.ID {
		t.Fatalf("fiche vivante = %+v, %v", fresh, err)
	}
	assertRematchStable(t, db)
}

// Plusieurs fiches d'une unité, plusieurs politiques : la plus récente qui en
// portait une gagne, une vivante avant une archivée.
func TestMigration024_PolicyMostRecentFicheWins(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	// web : une vivante « excluded » (plus ancienne) bat une archivée « pinned » plus récente.
	seedFiche(t, db, composeFiche("m1:web-live", m1, "shop", "web").with(func(f *fiche) { f.policy, f.lastSeen = "excluded", 1_700_000_000 }))
	seedFiche(t, db, composeFiche("m1:web-gone", m1, "shop", "web").with(func(f *fiche) { f.archived, f.policy, f.lastSeen = true, "pinned", 1_800_000_000 }))
	// db : deux archivées, la plus récente gagne.
	seedFiche(t, db, composeFiche("m1:db-old", m1, "shop", "db").with(func(f *fiche) { f.archived, f.policy, f.lastSeen = true, "excluded", 1_600_000_000 }))
	seedFiche(t, db, composeFiche("m1:db-newer", m1, "shop", "db").with(func(f *fiche) { f.archived, f.policy, f.lastSeen = true, "pinned", 1_700_000_000 }))
	// cache : une politique vide n'efface rien, elle n'en est pas une.
	seedFiche(t, db, composeFiche("m1:cache-live", m1, "shop", "cache").with(func(f *fiche) { f.lastSeen = 1_800_000_000 }))
	seedFiche(t, db, composeFiche("m1:cache-gone", m1, "shop", "cache").with(func(f *fiche) { f.archived, f.policy, f.lastSeen = true, "pinned", 1_700_000_000 }))
	// extra : aucune politique, mais un lien l'entraîne dans le service.
	seedFiche(t, db, composeFiche("m1:extra", m1, "shop", "extra"))
	seedProbe(t, db, "probe1", m1, "m1:extra")

	migrateToTheEnd(t, db)

	created := declaredNamed(t, db, m1, "shop")
	want := map[string]service.UpdatePolicy{"web": service.PolicyIgnore, "db": service.PolicyHold, "cache": service.PolicyHold, "extra": service.PolicyWatch}
	for name, policy := range want {
		if got := unitNamed(t, created, name).UpdatePolicy; got != policy {
			t.Errorf("unité %s : politique %q, want %q", name, got, policy)
		}
	}
}

// Deux conteneurs d'un même projet donnent le même service : un seul membre
// par composant, le premier dans l'ordre d'insertion, les autres membres à leur
// place. Un autre composant garde son propre membre.
func TestMigration024_ComponentMembersOfOneProjectBecomeOne(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, composeFiche("m1:app", m1, "nextcloud", "app"))
	seedFiche(t, db, composeFiche("m1:db", m1, "nextcloud", "db"))
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "")
	exec(t, db, `INSERT INTO heartbeats (id, token, name, status, interval_seconds, grace_seconds, created_at) VALUES ('hb1', 't', 'sauvegarde', 'new', 3600, 60, 1)`)
	seedComponent(t, db, "cmp1")
	seedComponent(t, db, "cmp2")
	// Ordre d'insertion : machine, app, sonde, db (doublon de app), whoami, tâche.
	seedMember(t, db, "cmp1", "machine_id", m1)
	seedMember(t, db, "cmp1", "service_id", "m1:app")
	seedMember(t, db, "cmp1", "probe_id", "probe1")
	seedMember(t, db, "cmp1", "service_id", "m1:db")
	seedMember(t, db, "cmp1", "service_id", "m1:whoami")
	seedMember(t, db, "cmp1", "heartbeat_id", "hb1")
	seedMember(t, db, "cmp2", "service_id", "m1:db")

	migrateToTheEnd(t, db)

	project := declaredNamed(t, db, m1, "nextcloud")
	lone := declaredNamed(t, db, m1, "whoami")
	rows, err := db.sql.Query(`
		SELECT component_id, COALESCE(machine_id, ''), COALESCE(service_id, ''), COALESCE(heartbeat_id, ''), COALESCE(probe_id, '')
		FROM status_component_members ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var component, machine, serviceID, heartbeat, probe string
		if err := rows.Scan(&component, &machine, &serviceID, &heartbeat, &probe); err != nil {
			t.Fatal(err)
		}
		got = append(got, strings.Join([]string{component, machine, serviceID, heartbeat, probe}, "|"))
	}
	want := []string{
		"cmp1|" + m1 + "|||",
		"cmp1||" + project.ID + "||",
		"cmp1||||probe1",
		"cmp1||" + lone.ID + "||",
		"cmp1|||hb1|",
		"cmp2||" + project.ID + "||",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("membres :\n got  %q\n want %q", got, want)
	}
	assertRematchStable(t, db)
}

// Ni sonde, ni composant, ni politique : rien n'est repris, les conteneurs
// restent non déclarés, l'annuaire reste vide.
func TestMigration024_UnlinkedContainersStayUndeclared(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, composeFiche("m1:app", m1, "nextcloud", "app"))
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedFiche(t, db, loneFiche("m2:gone", m2, "gone").with(func(f *fiche) { f.archived = true }))

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 0 {
		t.Fatalf("%d services créés pour rien", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM service_units`); got != 0 {
		t.Fatalf("%d unités créées pour rien", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM services WHERE unit_id IS NOT NULL`); got != 0 {
		t.Fatalf("%d fiches rattachées pour rien", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM services`); got != 3 {
		t.Fatalf("%d fiches, trois attendues", got)
	}
}

// Le service que l'opérateur a déclaré est réutilisé, jamais doublé. Un
// conteneur lié dont le service Compose lui manque lui ajoute une unité et le
// remet « à vérifier » ; sa provenance reste « opérateur ».
func TestMigration024_OperatorServiceReusedGainsUnitAndNeedsReview(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedDeclaredCompose(t, db, "svc_ops", m1, "nextcloud", true, "app")
	seedFiche(t, db, composeFiche("m1:app", m1, "nextcloud", "app"))
	seedFiche(t, db, composeFiche("m1:redis", m1, "nextcloud", "redis").with(func(f *fiche) { f.policy, f.image = "pinned", "redis:7.4" }))
	seedProbe(t, db, "probe1", m1, "m1:app")

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 1 {
		t.Fatalf("%d services, le service de l'opérateur doit être réutilisé", got)
	}
	reused := declaredNamed(t, db, m1, "nextcloud")
	if reused.ID != "svc_ops" || reused.DeclaredBy != directory.ByOperator || !reused.NeedsReview {
		t.Fatalf("service = %+v", reused)
	}
	if len(reused.Units) != 2 || reused.Units[0].Name != "app" || reused.Units[1].Name != "redis" {
		t.Fatalf("unités = %+v", reused.Units)
	}
	if redis := unitNamed(t, reused, "redis"); redis.UpdatePolicy != service.PolicyHold || redis.Image != "redis" || redis.Version != "7.4" {
		t.Fatalf("unité ajoutée = %+v", redis)
	}
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != "svc_ops" {
		t.Fatalf("la sonde vise %q", got)
	}
	if ficheUnit(t, db, "m1:app") != "unit_nextcloud_app" || ficheUnit(t, db, "m1:redis") != unitNamed(t, reused, "redis").ID {
		t.Fatal("les deux conteneurs doivent être rattachés")
	}
	assertRematchStable(t, db)
}

// Une fiche que l'inventaire avait déjà rattachée garde son unité, même si
// l'ordre habituel la rattacherait ailleurs ; la sonde suit le service de cette
// unité, et rien n'est créé.
func TestMigration024_AttachedFicheKeepsItsUnit(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedDeclaredCompose(t, db, "svc_ops", m1, "nextcloud", true, "app", "db")
	seedFiche(t, db, composeFiche("m1:app", m1, "nextcloud", "app").with(func(f *fiche) {
		f.unitID, f.level = "unit_nextcloud_db", "name"
	}))
	seedProbe(t, db, "probe1", m1, "m1:app")

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 1 {
		t.Fatalf("%d services, aucun n'est à créer", got)
	}
	if got := ficheUnit(t, db, "m1:app"); got != "unit_nextcloud_db" {
		t.Fatalf("la fiche a changé d'unité : %q", got)
	}
	if got := text(t, db, `SELECT match_level FROM services WHERE id = 'm1:app'`); got != "name" {
		t.Fatalf("le niveau a changé : %q", got)
	}
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != "svc_ops" {
		t.Fatalf("la sonde vise %q", got)
	}
	// Rien à relire : l'opérateur n'a rien vu changer à son service.
	if got := count(t, db, `SELECT needs_review FROM declared_services WHERE id = 'svc_ops'`); got != 0 {
		t.Fatal("un service que rien n'a modifié ne se remet pas à vérifier")
	}
}

// L'historique d'une sonde, ses alertes et leurs liens survivent à la
// reconstruction de la table : ce que DROP TABLE emporterait par cascade
// avec les clés étrangères en marche.
func TestMigration024_KeepsProbeHistoryAndAlerts(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "m1:whoami")
	seedProbe(t, db, "probe2", m1, "")
	seedProbeHistory(t, db, "probe1", 5, 3)
	seedProbeHistory(t, db, "probe2", 2, 1)
	seedAlert(t, db, "probe_down", "probe", "probe1", "probe1", "")
	seedAlert(t, db, "probe_slow", "probe", "probe2", "probe2", "")
	// L'alerte d'un conteneur garde la fiche : elle vise l'objet observé.
	seedAlert(t, db, "service_down", "service", "m1:whoami", "", "m1:whoami")
	seedComponent(t, db, "cmp1")
	seedMember(t, db, "cmp1", "probe_id", "probe1")
	counts := map[string]string{
		"sondes":            `SELECT COUNT(*) FROM probes`,
		"essais":            `SELECT COUNT(*) FROM probe_results`,
		"jours":             `SELECT COUNT(*) FROM probe_days`,
		"alertes de sonde":  `SELECT COUNT(*) FROM alerts WHERE probe_id IS NOT NULL`,
		"alertes de fiche":  `SELECT COUNT(*) FROM alerts WHERE service_id IS NOT NULL`,
		"membres sonde":     `SELECT COUNT(*) FROM status_component_members WHERE probe_id IS NOT NULL`,
		"somme des essais":  `SELECT SUM(duration_ms + checked_at) FROM probe_results`,
		"somme des jours":   `SELECT SUM(total + success + degraded + duration_ms) FROM probe_days`,
		"alertes (toutes)":  `SELECT COUNT(*) FROM alerts`,
		"sondes sans lien":  `SELECT COUNT(*) FROM probes WHERE service_id IS NULL`,
		"fiches de service": `SELECT COUNT(*) FROM services`,
	}
	before := map[string]int{}
	for name, query := range counts {
		before[name] = count(t, db, query)
	}
	if before["essais"] != 7 || before["jours"] != 4 || before["alertes de sonde"] != 2 {
		t.Fatalf("semis inattendu: %+v", before)
	}

	migrateToTheEnd(t, db)

	for name, query := range counts {
		want := before[name]
		if got := count(t, db, query); got != want {
			t.Errorf("%s : %d avant, %d après", name, want, got)
		}
	}
	if violations := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); violations != 0 {
		t.Fatalf("%d clés étrangères pendantes", violations)
	}
	// Les clés tiennent encore : la suppression d'une sonde emporte son historique, et lui seul.
	exec(t, db, `DELETE FROM probes WHERE id = 'probe2'`)
	if got := count(t, db, `SELECT COUNT(*) FROM probe_results`); got != 5 {
		t.Fatalf("la cascade doit emporter les essais de probe2 : %d restent", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alerts WHERE probe_id IS NULL AND object_id = 'probe2'`); got != 1 {
		t.Fatal("l'alerte d'une sonde supprimée garde son nom et perd son lien")
	}
}

// Base neuve : rien à reprendre, rien ne change, la migration est notée.
func TestMigration024_EmptyDatabaseChangesNothing(t *testing.T) {
	db := openAtVersion(t, 23)
	exec(t, db, `INSERT INTO machines (id, name, kind, enrolled_at, created_at) VALUES ('local', 'opencloud', 'local', 1, 1)`)
	schemaBefore := schemaOf(t, db, "probes", "status_component_members")

	migrateToTheEnd(t, db)

	for _, table := range []string{"declared_services", "service_units", "services", "probes", "status_component_members"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s : %d lignes", table, got)
		}
	}
	applied, err := db.AppliedVersions(context.Background())
	if err != nil || applied[len(applied)-1] != 24 {
		t.Fatalf("versions = %v, %v", applied, err)
	}
	// Les tables reconstruites gardent leur forme : seule la clé change.
	schemaAfter := schemaOf(t, db, "probes", "status_component_members")
	if strings.Count(schemaBefore, "REFERENCES services") != 2 || strings.Count(schemaAfter, "REFERENCES services") != 0 ||
		strings.Count(schemaAfter, "REFERENCES declared_services") != 2 {
		t.Fatalf("clés étrangères :\n avant %s\n après %s", schemaBefore, schemaAfter)
	}
	// Ni table temporaire, ni clé pendante ne reste derrière.
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_temp_master`); got != 0 {
		t.Fatalf("%d objets temporaires oubliés", got)
	}
}

func schemaOf(t *testing.T, db *DB, tables ...string) string {
	t.Helper()
	var all []string
	for _, table := range tables {
		all = append(all, text(t, db, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table))
	}
	return strings.Join(all, "\n")
}

// Tout ou rien : une base qui avait déjà une clé étrangère pendante, que le
// runner d'avant ne voyait pas, arrête la migration avec la table en cause et
// ne change rien — ni service créé, ni sonde repointée, ni version notée.
func TestMigration024_RefusesADatabaseWithADanglingKeyAndChangesNothing(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "m1:whoami")
	// Une seule connexion, pour que le pragma et l'insertion tombent sur elle.
	db.sql.SetMaxOpenConns(1)
	// Hors clés étrangères, comme le faisait une ancienne base : un jour sans sonde.
	exec(t, db, `PRAGMA foreign_keys = OFF`)
	exec(t, db, `INSERT INTO probe_days (probe_id, day, total, success, degraded, duration_ms) VALUES ('orpheline', 1, 1, 1, 0, 1)`)

	err := db.migrate(context.Background())

	if err == nil || !strings.Contains(err.Error(), "foreign key violated") || !strings.Contains(err.Error(), "probe_days") {
		t.Fatalf("attendu un refus qui nomme probe_days, obtenu %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 0 {
		t.Fatalf("%d services créés malgré le refus", got)
	}
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != "m1:whoami" {
		t.Fatalf("la sonde a été repointée malgré le refus : %q", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 24`); got != 0 {
		t.Fatal("la version 24 est notée malgré le refus")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_temp_master`); got != 0 {
		t.Fatalf("%d objets temporaires laissés par la transaction annulée", got)
	}
}

// Le compte de ce que la migration doit garder est contrôlé avant le COMMIT :
// un SQL qui perdrait de l'historique, ce que les clés étrangères ne voient
// pas, arrête tout. L'écart est joué avec la vraie migration, à laquelle on
// ajoute une perte.
func TestMigration024_StopsWhenItWouldLoseProbeHistory(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "m1:whoami")
	seedProbeHistory(t, db, "probe1", 4, 2)
	content, err := migrations.Files.ReadFile("024_directory_links.sql")
	if err != nil {
		t.Fatal(err)
	}

	lossy := string(content) + "\nDELETE FROM probe_results WHERE checked_at > 1700000120;\n"
	err = db.applyContent(context.Background(), "024_lossy.sql", 24, lossy, linkContainersToDirectory, verifyContainerLinks)

	if !errors.Is(err, errMigrationLostData) || !strings.Contains(err.Error(), "probe_results") {
		t.Fatalf("attendu errMigrationLostData sur probe_results, obtenu %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM probe_results`); got != 4 {
		t.Fatalf("%d essais restent, quatre attendus : la transaction doit être annulée", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 0 {
		t.Fatal("rien ne doit rester d'une migration annulée")
	}
	// La vraie migration, ensuite, passe sur la même base.
	migrateToTheEnd(t, db)
	if got := count(t, db, `SELECT COUNT(*) FROM probe_results`); got != 4 {
		t.Fatalf("%d essais après la vraie migration", got)
	}
}

// Une migration jouée deux fois ne fait rien de plus : la deuxième ouverture
// trouve la 024 notée et n'y touche pas.
func TestMigration024_IsAppliedOnce(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, loneFiche("m1:whoami", m1, "whoami"))
	seedProbe(t, db, "probe1", m1, "m1:whoami")
	migrateToTheEnd(t, db)
	first := declaredNamed(t, db, m1, "whoami")

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM declared_services`); got != 1 {
		t.Fatalf("%d services après la deuxième ouverture", got)
	}
	if second := declaredNamed(t, db, m1, "whoami"); second.ID != first.ID {
		t.Fatalf("le service a été recréé : %s -> %s", first.ID, second.ID)
	}
}

// Une sonde vise `app` ; le projet a aussi un conteneur d'initialisation terminé
// en 0 (non archivé). La migration ne crée d'unité que pour ce qui est lié ou
// tourne : `init` reste non déclaré, sinon le composant de statut passerait en
// panne le jour de la montée, sans que rien n'ait changé.
func TestMigration024_StoppedInitContainerStaysUndeclared(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedFiche(t, db, composeFiche("m1:app", m1, "cloud", "app"))
	seedFiche(t, db, composeFiche("m1:init", m1, "cloud", "init").with(func(f *fiche) { f.state = "exited" }))
	seedFiche(t, db, composeFiche("m1:worker", m1, "cloud", "worker"))
	seedComponent(t, db, "comp1")
	seedMember(t, db, "comp1", "service_id", "m1:app")
	seedProbe(t, db, "probe1", m1, "m1:app")

	migrateToTheEnd(t, db)

	created := declaredNamed(t, db, m1, "cloud")
	if len(created.Units) != 2 || unitNamed(t, created, "app").ID == "" || unitNamed(t, created, "worker").ID == "" {
		t.Fatalf("unités = %+v, app et worker attendus (init n'a pas d'unité)", created.Units)
	}
	if got := ficheUnit(t, db, "m1:init"); got != "" {
		t.Fatalf("init est rattaché à %q : il devait rester non déclaré", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM services WHERE id = 'm1:init'`); got != 1 {
		t.Fatal("la fiche d'init a disparu")
	}
	if got := text(t, db, `SELECT service_id FROM status_component_members WHERE component_id = 'comp1'`); got != created.ID {
		t.Fatalf("le membre vise %q, want %q", got, created.ID)
	}
	assertRematchStable(t, db)
}

// Un projet que l'opérateur a déclaré avec 32 unités, et une fiche liée d'un
// 33e service Compose du même projet. Avant la correction, le plan créait un
// second service Compose pour (machine, projet) : `UNIQUE constraint failed` et
// `serve` ne démarrait plus. Maintenant la fiche liée devient un conteneur seul.
func TestMigration024_FullDeclaredProjectDoesNotBlockStartup(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	var units []string
	for index := range directory.MaxUnits {
		units = append(units, fmt.Sprintf("u%02d", index))
	}
	seedDeclaredCompose(t, db, "svc_big", m1, "big", true, units...)
	seedFiche(t, db, composeFiche("m1:extra", m1, "big", "extra"))
	seedFiche(t, db, composeFiche("m1:other", m1, "big", "other"))
	seedProbe(t, db, "probe1", m1, "m1:extra")

	migrateToTheEnd(t, db)

	if got := count(t, db, `SELECT COUNT(*) FROM declared_services WHERE machine_id = ? AND compose_project = 'big'`, m1); got != 1 {
		t.Fatalf("%d services Compose pour le projet, un seul attendu", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM service_units WHERE service_id = 'svc_big'`); got != directory.MaxUnits {
		t.Fatalf("le service plein a %d unités", got)
	}
	target := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`)
	found, err := db.GetDeclaredService(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID == "svc_big" || found.Origin != directory.OriginContainer || found.DeclaredBy != directory.ByMigration || !found.NeedsReview {
		t.Fatalf("la sonde doit suivre un conteneur seul repris : %+v", found)
	}
	if found.Units[0].ContainerName != "big-extra-1" {
		t.Fatalf("unité = %+v", found.Units[0])
	}
	if err := (directory.ServiceDefinition{
		MachineID: found.MachineID, Name: found.Name, Nature: found.Nature, Origin: found.Origin,
		Units: []directory.UnitDefinition{{Name: found.Units[0].Name, Image: found.Units[0].Image, Version: found.Units[0].Version, ContainerName: found.Units[0].ContainerName}},
	}).Complete().Validate(); err != nil {
		t.Fatalf("le service créé ne passe pas Validate: %v", err)
	}
	if got := ficheUnit(t, db, "m1:other"); got != "" {
		t.Fatalf("la fiche sans lien est rattachée à %q", got)
	}
}

// Le projet déclaré par l'opérateur sur une autre machine ne reçoit pas la
// sonde d'un conteneur de la première : la clé du projet comprend la machine.
func TestMigration024_ProjectDeclaredOnAnotherMachineIsNotReused(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedDeclaredCompose(t, db, "svc_cloud_m2", m2, "cloud", true, "app")
	seedFiche(t, db, composeFiche("m1:app", m1, "cloud", "app"))
	seedProbe(t, db, "probe1", m1, "m1:app")

	migrateToTheEnd(t, db)

	if got := text(t, db, `SELECT s.machine_id FROM probes p JOIN declared_services s ON s.id = p.service_id`); got != m1 {
		t.Fatalf("la sonde vise un service de %q, want %q", got, m1)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM service_units WHERE service_id = 'svc_cloud_m2'`); got != 1 {
		t.Fatal("le service de l'autre machine a bougé")
	}
}

// Une fiche archivée que rien ne vise, mais que le rapprochement rattache à un
// service de l'opérateur, reçoit son unité : l'inventaire ne recalcule jamais
// une fiche archivée, elle resterait sans « était rattaché à ».
func TestMigration024_ArchivedFicheOfADeclaredServiceIsAttached(t *testing.T) {
	db := openAtVersion(t, 23)
	seedMachines(t, db)
	seedDeclaredCompose(t, db, "svc_cloud", m1, "cloud", true, "app")
	seedFiche(t, db, composeFiche("m1:app", m1, "cloud", "app"))
	seedFiche(t, db, composeFiche("m1:old", m1, "cloud", "app").with(func(f *fiche) { f.archived = true }))
	seedProbe(t, db, "probe1", m1, "m1:app")

	migrateToTheEnd(t, db)

	if got := ficheUnit(t, db, "m1:old"); got != "unit_cloud_app" {
		t.Fatalf("la fiche archivée est à %q", got)
	}
	if got := text(t, db, `SELECT service_id FROM probes WHERE id = 'probe1'`); got != "svc_cloud" {
		t.Fatalf("la sonde vise %q", got)
	}
}
