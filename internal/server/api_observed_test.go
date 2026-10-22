package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// observedOf rend, pour chaque conteneur, si l'API le dit observé, et le
// dernier signal de sa machine tel que la réponse le donne.
func observedOf(t *testing.T, server *testServer, path string) (observed []bool, lastSeen []*time.Time) {
	t.Helper()
	recorder := callAPI(server.Server, http.MethodGet, path, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s : %d %s", path, recorder.Code, recorder.Body.String())
	}
	var response struct {
		Containers []struct {
			Observed          bool       `json:"observed"`
			MachineLastSeenAt *time.Time `json:"machine_last_seen_at"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, item := range response.Containers {
		observed = append(observed, item.Observed)
		lastSeen = append(lastSeen, item.MachineLastSeenAt)
	}
	return observed, lastSeen
}

func countsOfAttention(t *testing.T, server *testServer) (total, attention int) {
	t.Helper()
	recorder := callAPI(server.Server, http.MethodGet, "/api/counts", nil)
	var counts struct {
		Containers struct {
			Total     int `json:"total"`
			Attention int `json:"attention"`
		} `json:"containers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	return counts.Containers.Total, counts.Containers.Attention
}

// Une machine qui ne signale plus rend ses conteneurs non observés, partout
// où l'API les montre (liste, liste d'une machine, fiche, réseau), avec le
// dernier signal de la machine ; la barre latérale les compte au total mais
// plus à traiter. La borne est celle de machine.LostAfter.
func TestContainersAPI_ObservedFollowsTheLastSignalOfTheMachine(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	dbContainer := server.seedContainers(t)
	paths := []string{
		"/api/containers",
		"/api/machines/" + remoteID + "/containers",
		"/api/machines/" + remoteID + "/network",
	}
	for _, step := range []struct {
		name          string
		signal        time.Time
		observed      bool
		wantAttention int
	}{
		{"jamais vue", time.Time{}, false, 0},
		{"vue il y a cinq heures", testNow.Add(-5 * time.Hour), false, 0},
		{"la borne", testNow.Add(-machine.LostAfter), true, 1},
		{"une seconde de trop", testNow.Add(-machine.LostAfter - time.Second), false, 0},
		{"à l'instant", testNow, true, 1},
	} {
		t.Run(step.name, func(t *testing.T) {
			if !step.signal.IsZero() {
				server.signaledAt(t, remoteID, step.signal)
			}
			for _, path := range paths {
				observed, lastSeen := observedOf(t, server, path)
				if len(observed) != 2 {
					t.Fatalf("%s : %d conteneurs", path, len(observed))
				}
				for i := range observed {
					if observed[i] != step.observed {
						t.Errorf("%s : conteneur %d observé = %v, attendu %v", path, i, observed[i], step.observed)
					}
					if step.signal.IsZero() != (lastSeen[i] == nil) || (lastSeen[i] != nil && !lastSeen[i].Equal(step.signal)) {
						t.Errorf("%s : dernier signal %v, attendu %v", path, lastSeen[i], step.signal)
					}
				}
			}
			var fiche struct {
				Container struct {
					Observed bool `json:"observed"`
				} `json:"container"`
			}
			recorder := callAPI(server.Server, http.MethodGet, "/api/containers/"+dbContainer, nil)
			if err := json.Unmarshal(recorder.Body.Bytes(), &fiche); err != nil || fiche.Container.Observed != step.observed {
				t.Errorf("fiche : observé = %v (%v), attendu %v", fiche.Container.Observed, err, step.observed)
			}
			total, attention := countsOfAttention(t, server)
			if total != 2 || attention != step.wantAttention {
				t.Errorf("compteurs : %d au total, %d à traiter, attendu 2 et %d", total, attention, step.wantAttention)
			}
		})
	}
}

// L'écart d'un service déclaré dit si la machine qui le porte est observée.
func TestDirectoryGapsAPI_ObservedFollowsTheMachineOfTheService(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	cloud := server.seedGaps(t)
	read := func() map[string]bool {
		recorder := callAPI(server.Server, http.MethodGet, "/api/directory/gaps", nil)
		var response gapsResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		byService := map[string]bool{}
		for _, gap := range response.Services {
			byService[gap.ServiceID] = gap.Observed
		}
		return byService
	}
	if observed := read(); observed[cloud] || len(observed) != 2 {
		t.Fatalf("machine jamais vue : %v", observed)
	}
	server.signaledAt(t, remoteID, testNow)
	observed := read()
	if !observed[cloud] {
		t.Fatalf("machine vue à l'instant : %v", observed)
	}
	// Le service de la machine openCloud, qui n'a jamais signalé, reste non observé.
	for id, ok := range observed {
		if id != cloud && ok {
			t.Fatalf("la machine openCloud n'a jamais signalé : %v", observed)
		}
	}
}
