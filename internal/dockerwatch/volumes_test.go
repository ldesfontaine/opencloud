package dockerwatch

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
)

const volumeInspect = `{"Id":"` + fixtureID + `","Name":"/web","Image":"sha256:abc","Config":{"Image":"alpine:3.20"},
	"State":{"Status":"running"},
	"Mounts":[{"Type":"volume","Name":"data","Driver":"local"},{"Type":"bind","Source":"/srv/site"},{"Type":"volume","Name":"data"}]}`

func volumeDaemon(volumesStatus int) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1.41/containers/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"` + fixtureID + `","Names":["/web"],"Labels":{}}]`))
	})
	mux.HandleFunc("GET /v1.41/containers/"+fixtureID+"/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(volumeInspect))
	})
	mux.HandleFunc("GET /v1.41/networks", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	mux.HandleFunc("GET /v1.41/volumes", func(w http.ResponseWriter, r *http.Request) {
		if volumesStatus != http.StatusOK {
			w.WriteHeader(volumesStatus)
			_, _ = w.Write([]byte(`{"message":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"Volumes":[{"Name":"data","Driver":"local","Mountpoint":"/var/lib/docker/volumes/data/_data"},{"Name":"spare","Driver":"local"}],"Warnings":null}`))
	})
	return mux
}

// L'inventaire livre les volumes nommés avec leurs conteneurs ; un dossier de
// l'hôte n'en est pas un, et le point de montage ne part pas.
func TestDeliverInventory_ReportsVolumesAndTheirUsers(t *testing.T) {
	sink := newSink()
	newWatcher(serve(t, volumeDaemon(http.StatusOK)), sink).deliverInventory(context.Background())

	report := sink.waitFor(t, "inventory", func(r container.Report) bool { return r.Complete })
	want := []container.VolumeReport{{Name: "data", Driver: "local", UsedBy: []string{"web"}}, {Name: "spare", Driver: "local"}}
	if !report.VolumesComplete || !slices.EqualFunc(report.Volumes, want, func(a, b container.VolumeReport) bool {
		return a.Name == b.Name && a.Driver == b.Driver && slices.Equal(a.UsedBy, b.UsedBy)
	}) {
		t.Fatalf("volumes = %+v", report.Volumes)
	}
}

// Un démon qui ne liste pas les volumes ne fait pas perdre l'inventaire : la
// liste n'est pas dite complète, le serveur garde ce qu'il avait.
func TestDeliverInventory_WithoutVolumesKeepsTheInventory(t *testing.T) {
	sink := newSink()
	newWatcher(serve(t, volumeDaemon(http.StatusInternalServerError)), sink).deliverInventory(context.Background())

	report := sink.waitFor(t, "inventory", func(r container.Report) bool { return r.Complete })
	if len(report.Inventory) != 1 || report.VolumesComplete || len(report.Volumes) != 0 {
		t.Fatalf("report = %+v", report)
	}
}
