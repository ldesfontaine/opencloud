package store

import (
	"context"
	"testing"
)

// La 036 sur une base existante (observe est toujours rapporté avec les droits), à jour de la 035 : l'agent déjà enrôlé et ses
// droits restent tels quels, ses ancres valent « pas rapportées » (NULL : un
// agent d'avant les ancres ne les applique pas, le serveur ne juge rien), puis
// l'ouverture du flux suivant les écrit, et un texte vide les efface.
func TestMigration036_KeepsTheAgentAndStartsWithoutReportedAnchors(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 36 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('pve', 'Proxmox', 'pve', 1)`)
	exec(t, db, `INSERT INTO agents (id, machine_id, public_key, version, enrolled_at) VALUES ('agt_1', 'pve', zeroblob(32), 'v0.6.0', 2)`)
	exec(t, db, `INSERT INTO agent_capabilities (agent_id, capability, scope, reported_at) VALUES ('agt_1', 'backup.produce', 'proxmox-vzdump', 3)`)

	if err := db.apply(ctx, "036_agent_anchors.sql", 36); err != nil {
		t.Fatal(err)
	}

	found, err := db.GetMachine(ctx, "pve")
	if err != nil {
		t.Fatal(err)
	}
	if found.Agent.ID != "agt_1" || found.Agent.Version != "v0.6.0" || len(found.Agent.Capabilities) != 2 {
		t.Fatalf("the agent changed: %+v", found.Agent)
	}
	if found.Agent.Anchors.Reported {
		t.Fatalf("an agent that never spoke of anchors must not look as if it reported none: %+v", found.Agent.Anchors)
	}

	if err := db.RecordAnchors(ctx, "agt_1", "backup.guest=101:102"); err != nil {
		t.Fatal(err)
	}
	found, _ = db.GetMachine(ctx, "pve")
	if !found.Agent.Anchors.Reported || len(found.Agent.Anchors.Values["backup.guest"]) != 2 {
		t.Fatalf("after the report: %+v", found.Agent.Anchors)
	}
	if err := db.RecordAnchors(ctx, "agt_1", "none"); err != nil {
		t.Fatal(err)
	}
	found, _ = db.GetMachine(ctx, "pve")
	if !found.Agent.Anchors.Reported || found.Agent.Anchors.HasAny() {
		t.Fatalf("an agent that reports none: %+v", found.Agent.Anchors)
	}
	if err := db.RecordAnchors(ctx, "agt_1", ""); err != nil {
		t.Fatal(err)
	}
	found, _ = db.GetMachine(ctx, "pve")
	if found.Agent.Anchors.Reported {
		t.Fatalf("an empty text erases the report: %+v", found.Agent.Anchors)
	}
	if err := db.RecordAnchors(ctx, "agt_unknown", "none"); err == nil {
		t.Fatal("an unknown agent must not be noted")
	}
}
