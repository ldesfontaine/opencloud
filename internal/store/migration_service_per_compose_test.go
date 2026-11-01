package store

import (
	"context"
	"testing"
)

// La 028 sur une base qui a déjà un service « edge » de trois unités
// (traefik, authelia, crowdsec) : rien ne change pour lui, l'index unique du
// projet Compose disparaît, la catégorie naît vide (elle se déduit à la
// lecture).
func TestMigration028_KeepsEdgeAndLiftsProjectIndex(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 28 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_edge', 'vm-edge', 1)`)
	exec(t, db, `INSERT INTO declared_services (id, machine_id, name, title, nature, origin, compose_project, public_url, declared_by, created_at, updated_at)
		VALUES ('svc_edge', 'm_edge', 'edge', 'Frontal', 'infra', 'compose', 'edge', 'https://edge.example.org', 'operator', 1, 1)`)
	exec(t, db, `INSERT INTO service_units (id, service_id, name, kind, image, version, ports, position) VALUES
		('unit_t', 'svc_edge', 'traefik', 'container', 'traefik', 'v3.1', '["80:80","443:443"]', 0),
		('unit_a', 'svc_edge', 'authelia', 'container', 'authelia/authelia', '4.38', '[]', 1),
		('unit_c', 'svc_edge', 'crowdsec', 'container', 'crowdsecurity/crowdsec', 'v1.6', '[]', 2)`)
	exec(t, db, `INSERT INTO service_provides (service_id, capability) VALUES ('svc_edge', 'proxy'), ('svc_edge', 'sso')`)

	// Avant : un second service sur le même projet est refusé.
	insertSecond := `INSERT INTO declared_services (id, machine_id, name, nature, origin, compose_project, declared_by, created_at, updated_at)
		VALUES ('svc_second', 'm_edge', 'crowdsec-seul', 'application', 'compose', 'edge', 'operator', 1, 1)`
	if _, err := db.sql.Exec(insertSecond); err == nil {
		t.Fatal("avant la 028, deux services ne partagent pas un projet Compose")
	}

	if err := db.apply(ctx, "028_service_per_compose_service.sql", 28); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT name || '|' || title || '|' || nature || '|' || compose_project || '|' || public_url || '|' || category FROM declared_services WHERE id = 'svc_edge'`); got != "edge|Frontal|infra|edge|https://edge.example.org|" {
		t.Errorf("le service edge a changé : %q", got)
	}
	if got := text(t, db, `SELECT group_concat(name || '=' || image || ':' || version, ',') FROM (SELECT * FROM service_units WHERE service_id = 'svc_edge' ORDER BY position)`); got != "traefik=traefik:v3.1,authelia=authelia/authelia:4.38,crowdsec=crowdsecurity/crowdsec:v1.6" {
		t.Errorf("les unités ont changé : %q", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM service_provides WHERE service_id = 'svc_edge'`); got != 2 {
		t.Errorf("%d fournitures, 2 attendues", got)
	}
	// Après : deux services sur le même projet, mais le nom reste unique.
	exec(t, db, insertSecond)
	if _, err := db.sql.Exec(`INSERT INTO declared_services (id, machine_id, name, nature, origin, compose_project, declared_by, created_at, updated_at)
		VALUES ('svc_third', 'm_edge', 'crowdsec-seul', 'application', 'compose', 'edge', 'operator', 1, 1)`); err == nil {
		t.Error("le nom d'un service reste unique par machine")
	}
}
