package restic

import (
	"fmt"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Le contenu d'un service : son dossier Compose, les volumes qu'un de ses
// conteneurs monte (un volume d'un autre service n'y entre pas), ses
// conteneurs de base par l'image ; triés, sans nom qui ne soit pas Docker.
func TestDeriveContent_TakesTheFolderTheMountedVolumesAndTheDatabases(t *testing.T) {
	service := ServiceRef{ID: "svc", Project: "nextcloud"}
	docker := ServiceDocker{
		Containers: []DockerContainer{
			{Name: "nextcloud-db-1", Image: "docker.io/library/postgres:16"},
			{Name: "nextcloud-app-1", Image: "nextcloud:29"},
			{Name: "nextcloud-cache-1", Image: "redis:7"},
			{Name: "../evil", Image: "mariadb:11"},
		},
		Volumes: []DockerVolume{
			{Name: "nextcloud_html", UsedBy: []string{"nextcloud-app-1"}},
			{Name: "nextcloud_db", UsedBy: []string{"nextcloud-db-1"}},
			{Name: "gitea_data", UsedBy: []string{"gitea-1"}},
			{Name: "orphan"},
			{Name: "a/b", UsedBy: []string{"nextcloud-app-1"}},
		},
	}
	content, isTruncated := deriveContent(service, docker)
	if isTruncated || content.Folder != "nextcloud" || !slices.Equal(content.Volumes, []string{"nextcloud_db", "nextcloud_html"}) {
		t.Fatalf("content %+v truncated %v", content, isTruncated)
	}
	if !slices.Equal(content.Databases, []Database{{Container: "nextcloud-db-1", Engine: agentwire.EnginePostgres}}) {
		t.Fatalf("databases %+v", content.Databases)
	}
	if !content.NeedsDocker() || content.IsEmpty() {
		t.Fatal("volumes and databases need restic.docker")
	}
}

// Sans dossier Compose (un conteneur seul), le service garde ses volumes et
// ses bases ; sans rien, il est vide et ne demande aucun droit Docker.
func TestDeriveContent_AServiceWithoutFolder(t *testing.T) {
	alone := ServiceDocker{Containers: []DockerContainer{{Name: "pg", Image: "postgres"}}}
	content, _ := deriveContent(ServiceRef{ID: "svc"}, alone)
	if content.Folder != "" || len(content.Databases) != 1 || content.IsEmpty() {
		t.Fatalf("content %+v", content)
	}
	empty, _ := deriveContent(ServiceRef{ID: "svc", Project: ".hidden"}, ServiceDocker{})
	if !empty.IsEmpty() || empty.NeedsDocker() {
		t.Fatalf("empty %+v", empty)
	}
	folderOnly, _ := deriveContent(ServiceRef{ID: "svc", Project: "blog"}, ServiceDocker{})
	if folderOnly.IsEmpty() || folderOnly.NeedsDocker() {
		t.Fatalf("folder only %+v", folderOnly)
	}
}

// Au-delà de la borne de l'ordre, le contenu est coupé (et le dit) : les
// paramètres restent valides pour l'agent.
func TestDeriveContent_IsBoundedByTheOrder(t *testing.T) {
	docker := ServiceDocker{}
	for index := range maxServiceParts + 5 {
		name := fmt.Sprintf("db-%02d", index)
		docker.Containers = append(docker.Containers, DockerContainer{Name: name, Image: "mariadb:11"})
		docker.Volumes = append(docker.Volumes, DockerVolume{Name: "vol-" + name, UsedBy: []string{name}})
	}
	content, isTruncated := deriveContent(ServiceRef{ID: "svc", Project: "big"}, docker)
	if !isTruncated || len(content.Volumes) != maxServiceParts || len(content.Databases) != maxServiceParts || content.Volumes[0] != "vol-db-00" {
		t.Fatalf("truncated %v: %d volumes, %d databases", isTruncated, len(content.Volumes), len(content.Databases))
	}
	params, err := backupParams(Backup{Scope: agentwire.ResticScopeService, TargetMachineID: "22222222-3333-4444-8555-666666666666"}, content)
	if err != nil {
		t.Fatalf("params %s: %v", params, err)
	}
}

// Un service qui n'a que son dossier part avec les paramètres d'avant :
// aucun champ de volume ni de base.
func TestBackupParams_AFolderOnlyServiceIsUnchanged(t *testing.T) {
	params, err := backupParams(Backup{Scope: agentwire.ResticScopeService, TargetMachineID: "22222222-3333-4444-8555-666666666666"}, ServiceContent{Folder: "nextcloud", Volumes: []string{}, Databases: []Database{}})
	want := `{"target":"22222222-3333-4444-8555-666666666666","scope":"service","service":"nextcloud"}`
	if err != nil || string(params) != want {
		t.Fatalf("params %s, want %s (%v)", params, want, err)
	}
}
