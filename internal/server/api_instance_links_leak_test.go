package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// Les liens ne gardent que des clés d'instance (projet, service, nom) : un
// secret semé là où un conteneur peut en porter (valeur d'un label, mot de
// passe dans une URL de réseau, dépendance) n'arrive dans aucun lien, ni dans
// aucune réponse qui les lit. L'agent ne lit d'ailleurs pas l'environnement
// (Env n'est pas déclaré dans le décodeur de dockerwatch).
func TestAPI_Links_SeededSecret_NeverInAnyOutput(t *testing.T) {
	const secret = "hunter2-SECRET-VALUE"
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	app, _, _ := server.seedLinkedServices(t)

	web := container.Observed{
		ContainerID: strings.Repeat("e", 64), Name: "worker", Group: "nextcloud", Image: "nextcloud:29.0.4", ImageID: "sha256:" + strings.Repeat("3", 64),
		ComposeService: "worker", ComposeDir: "/srv/nextcloud", ComposeFile: "/srv/nextcloud/compose.yaml", State: container.StateRunning,
		Labels:    map[string]string{"DB_PASSWORD": secret, "com.docker.compose.project": "nextcloud", container.LabelService: "svc_0000000000000000"},
		DependsOn: []container.Dependency{{Name: "nextcloud-db", Source: container.DependencyCompose}},
		CreatedAt: testNow.Add(-24 * time.Hour),
	}
	database := container.Observed{
		ContainerID: strings.Repeat("b", 64), Name: "nextcloud-db", Group: "nextcloud", Image: "postgres:16.4", ImageID: "sha256:" + strings.Repeat("2", 64),
		ComposeService: "nextcloud-db", State: container.StateRunning, CreatedAt: testNow.Add(-24 * time.Hour),
	}
	report := container.Report{Complete: true, Inventory: []container.Observed{web, database}}
	if err := server.containers.Record(context.Background(), remoteID, report); err != nil {
		t.Fatal(err)
	}

	stored, err := server.db.ListLinks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	dump, _ := json.Marshal(stored)
	outputs := []string{string(dump)}
	for _, path := range []string{"/api/directory/services/" + app + "/links", "/api/network", "/api/machines/" + remoteID + "/network"} {
		got := callAPI(server.Server, http.MethodGet, path, nil)
		if got.Code != http.StatusOK {
			t.Fatalf("%s : %d", path, got.Code)
		}
		outputs = append(outputs, got.Body.String())
	}
	if !strings.Contains(outputs[0], "compose:nextcloud/worker") {
		t.Fatalf("le lien du conteneur semé devait exister : %s", outputs[0])
	}
	for _, output := range outputs {
		if strings.Contains(output, secret) {
			t.Errorf("le secret semé est dans une sortie : %.200s", output)
		}
	}
}
