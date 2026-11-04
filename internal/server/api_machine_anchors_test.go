package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Les ancres que l'agent dit à l'ouverture de son flux sont gardées et rendues
// par la fiche de la machine, avec trois états distincts par type : posées,
// aucune posée, illisibles ; et un quatrième pour l'agent qui n'en dit rien.
func TestMachine_SaysTheAnchorsItsAgentReports(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "pve", remoteID)

	anchorsOf := func() anchorsJSON {
		recorder := callAPI(server.Server, http.MethodGet, "/api/machines", nil)
		var response struct {
			Machines []struct {
				ID    string `json:"id"`
				Agent *struct {
					Anchors anchorsJSON `json:"anchors"`
				} `json:"agent"`
			} `json:"machines"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		for _, found := range response.Machines {
			if found.ID == enrolled.ID && found.Agent != nil {
				return found.Agent.Anchors
			}
		}
		t.Fatal("machine not listed")
		return anchorsJSON{}
	}
	stateOf := func(anchors anchorsJSON, name string) string {
		for _, kind := range anchors.Kinds {
			if kind.Name == name {
				return kind.State
			}
		}
		return ""
	}

	// Un agent qui n'a rien dit (v0.6.0) : non rapportées, le serveur ne juge rien.
	silent := anchorsOf()
	if silent.Reported || len(silent.Kinds) != len(anchor.Names()) {
		t.Fatalf("silent agent: %+v", silent)
	}

	resp, _, _, cancel := servertest.OpenStreamWithAnchors(t, server.Server, enrolled.ID, private, "backup.guest=101:102,restic.target-cert=!")
	reported := anchorsOf()
	cancel()
	resp.Body.Close()
	if !reported.Reported || stateOf(reported, "backup.guest") != "set" || stateOf(reported, "restic.target-cert") != "unreadable" || stateOf(reported, "backup.recipient") != "none" {
		t.Fatalf("reported: %+v", reported)
	}
	var guests []string
	for _, kind := range reported.Kinds {
		if kind.Name == "backup.guest" {
			guests = kind.Values
		}
	}
	if !slices.Equal(guests, []string{"101", "102"}) {
		t.Fatalf("guests %v", guests)
	}

	// Un texte qui ne se lit pas est ignoré en entier : l'état redevient « non rapporté ».
	resp, _, _, cancel = servertest.OpenStreamWithAnchors(t, server.Server, enrolled.ID, private, "backup.guest=0101")
	garbled := anchorsOf()
	cancel()
	resp.Body.Close()
	if garbled.Reported {
		t.Fatalf("garbled header: %+v", garbled)
	}

	resp, _, _, cancel = servertest.OpenStreamWithAnchors(t, server.Server, enrolled.ID, private, "none")
	none := anchorsOf()
	cancel()
	resp.Body.Close()
	if !none.Reported || stateOf(none, "backup.guest") != "none" {
		t.Fatalf("none: %+v", none)
	}
}
