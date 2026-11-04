package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/restic"
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

// Une protection dont la dernière passe est refusée par une ancre porte la
// commande à lancer : exacte pour une VM d'un plan par archives (sa VM, la clé
// du trousseau), « trust list » sinon (restic : la valeur n'est pas connue).
func TestProtectionFix_ExactForAnArchivePlanListOtherwise(t *testing.T) {
	const recipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
	archive := restic.Protection{Origin: restic.OriginArchive, Reason: "anchor_missing:backup.guest"}
	command, exact := protectionFix(archive, restic.Source{VMID: 5012}, recipient)
	if !exact || !strings.HasSuffix(command, " agent trust add backup.guest 5012") {
		t.Fatalf("%q %v", command, exact)
	}
	archive.Reason = "anchor_outside:backup.recipient"
	if command, exact = protectionFix(archive, restic.Source{VMID: 5012}, recipient); !exact || !strings.HasSuffix(command, " agent trust add backup.recipient "+recipient) {
		t.Fatalf("%q %v", command, exact)
	}
	if command, exact = protectionFix(archive, restic.Source{VMID: 5012}, ""); exact || !strings.HasSuffix(command, " agent trust list") {
		t.Fatalf("no keyring: %q %v", command, exact)
	}
	resticOrigin := restic.Protection{Origin: restic.OriginRestic, Reason: "anchor_missing:restic.target-cert"}
	if command, exact = protectionFix(resticOrigin, restic.Source{VMID: 5012}, recipient); exact || !strings.HasSuffix(command, " agent trust list") {
		t.Fatalf("restic: %q %v", command, exact)
	}
	if command, _ = protectionFix(restic.Protection{Reason: "timeout"}, restic.Source{}, recipient); command != "" {
		t.Fatalf("another reason has no fix: %q", command)
	}
}
