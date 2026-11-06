package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
)

func aHostReport() hostinfo.Report {
	return hostinfo.Report{
		Virt:       "kvm",
		Interfaces: []hostinfo.Interface{{Name: "eth0", MAC: "bc:24:11:aa:bb:cc"}},
		Storage: hostinfo.Storage{
			Disks: []hostinfo.Disk{
				{Name: "sda", Model: "WDC WD10EZEX", Size: 1000204886016, Media: hostinfo.MediaHDD},
				{Name: "nvme0n1", Model: "Samsung SSD 980", Size: 500107862016, Media: hostinfo.MediaNVMe},
			},
			Pools: []hostinfo.Pool{
				{Name: "md0", Kind: hostinfo.PoolMD, Level: "raid1", State: hostinfo.PoolDegraded, Size: 1000204886016, Members: []string{"sda"}},
			},
		},
	}
}

// Le signal de l'agent dit où tourne la machine et ce qu'elle a comme disques ;
// sans hôte connu, une machine virtualisée est un VPS.
func TestPlacement_TheSignalWritesTheKindAndTheStorage(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "edge", remoteID)
	report := aHostReport()
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	var response machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &response)
	if response.Kind != "vps" || response.KindOverride != "" || response.Host != nil {
		t.Fatalf("kind %q override %q host %+v", response.Kind, response.KindOverride, response.Host)
	}
	storage := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/storage", nil)
	if storage.Code != http.StatusOK {
		t.Fatalf("stockage: %d %s", storage.Code, storage.Body.String())
	}
	goldentest.Compare(t, filepath.Join("testdata", "machine_storage.golden.json"), normalize(storage.Body.String()))

	// Un disque que l'agent ne voit plus disparaît.
	report.Disks, report.Pools = report.Disks[:1], nil
	server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report})
	var after storageJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/storage", nil), &after)
	if len(after.Disks) != 1 || len(after.Pools) != 0 {
		t.Fatalf("après: %+v", after)
	}
}

func TestPlacement_AnUnknownMachineHasNoStorage(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/nope/storage", nil); got.Code != http.StatusNotFound {
		t.Fatalf("got %d", got.Code)
	}
}

// Un rapport hors bornes est refusé seul, et l'ancien reste.
func TestPlacement_AnInvalidReportIsRefusedAloneAndTheOldOneStays(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "edge", remoteID)
	good := aHostReport()
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &good}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	hostile := map[string]func(*hostinfo.Report){
		"virt hostile":  func(r *hostinfo.Report) { r.Virt = `kvm"><script>` },
		"MAC hostile":   func(r *hostinfo.Report) { r.Interfaces[0].MAC = "zz" },
		"média inconnu": func(r *hostinfo.Report) { r.Disks[0].Media = "tape" },
		"pool inconnu":  func(r *hostinfo.Report) { r.Pools[0].Kind = "ceph" },
		"nom de disque": func(r *hostinfo.Report) { r.Disks[0].Name = "../../etc" },
	}
	for name, mutate := range hostile {
		t.Run(name, func(t *testing.T) {
			report := aHostReport()
			mutate(&report)
			got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report})
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_host") {
				t.Fatalf("rapport hostile accepté : %d %s", got.Code, got.Body.String())
			}
		})
	}
	var kept storageJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/storage", nil), &kept)
	if len(kept.Disks) != 2 {
		t.Fatalf("le rapport a changé : %+v", kept)
	}
}

// Rien ne s'écrit sur le journal d'audit : c'est une lecture de l'agent.
func TestPlacement_TheAgentReportIsNotAudited(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "edge", remoteID)
	before := server.latestAuditID(t)
	report := aHostReport()
	server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report})
	if events := server.auditSince(t, before); len(events) != 0 {
		t.Fatalf("événements : %+v", events)
	}
}

// L'opérateur corrige le type et l'hôte ; la correction l'emporte sur ce que
// l'agent rapporte, et l'agent ne la défait pas.
func TestPlacement_TheOperatorCorrectsKindAndHostAndTheAgentDoesNotUndoIt(t *testing.T) {
	server := newTestServer(t)
	token := connectAgent(t, server, "edge", remoteID)
	server.enroll(t, "hyper", dockerVMID)
	report := aHostReport()
	server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report})

	path := "/api/machines/" + remoteID + "/placement"
	got := callAPI(server.Server, http.MethodPut, path, map[string]any{"kind_override": "physical", "host_override": dockerVMID})
	var response machineJSON
	decodeAPI(t, got, &response)
	if got.Code != http.StatusOK || response.Kind != "physical" || response.KindOverride != "physical" {
		t.Fatalf("%d %+v", got.Code, response)
	}
	if response.Host == nil || response.Host.MachineID != dockerVMID || response.Host.MachineName != "hyper" || response.Host.Source != "override" {
		t.Fatalf("hôte : %+v", response.Host)
	}

	server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report})
	var after machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &after)
	if after.Kind != "physical" || after.Host == nil {
		t.Fatalf("l'agent a défait la correction : %+v", after)
	}

	callAPI(server.Server, http.MethodPut, path, map[string]any{"kind_override": "", "host_override": ""})
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil), &after)
	if after.Kind != "vps" || after.Host != nil {
		t.Fatalf("la correction retirée, le type déduit revient : %+v", after)
	}

	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectMachine})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == audit.MachinePlacement {
			count++
			if event.Actor.Kind != audit.ActorOperator {
				t.Fatalf("acteur %+v", event.Actor)
			}
		}
	}
	if count != 2 {
		t.Fatalf("%d événements de correction, 2 attendus", count)
	}
}

func TestPlacement_RefusesWhatCannotBeTrueAndTokensNeverCorrect(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "edge", remoteID)
	full, _ := server.issueWriteToken(t, "plein", allVerbIDs()...)
	path := "/api/machines/" + remoteID + "/placement"

	if got := callBearerWith(server.Server, http.MethodPut, path, full, ""); got.Code != http.StatusForbidden || errorCode(t, got) != codeAPITokenRouteForbidden {
		t.Fatalf("un jeton a corrigé : %d %s", got.Code, got.Body.String())
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/storage", full, ""); got.Code != http.StatusForbidden {
		t.Fatalf("un jeton a lu le stockage : %d", got.Code)
	}
	cases := map[string]struct {
		body map[string]any
		path string
		code int
		key  string
	}{
		"type inconnu":     {map[string]any{"kind_override": "mainframe"}, path, http.StatusUnprocessableEntity, "machine.kind_invalid"},
		"hôte inconnu":     {map[string]any{"host_override": "nope"}, path, http.StatusUnprocessableEntity, "machine.host_invalid"},
		"elle-même":        {map[string]any{"host_override": remoteID}, path, http.StatusUnprocessableEntity, "machine.host_invalid"},
		"machine inconnue": {map[string]any{}, "/api/machines/nope/placement", http.StatusNotFound, ""},
	}
	for name, test := range cases {
		got := callAPI(server.Server, http.MethodPut, test.path, test.body)
		if got.Code != test.code || (test.key != "" && errorCode(t, got) != test.key) {
			t.Errorf("%s : %d %s", name, got.Code, got.Body.String())
		}
	}
}

// Une machine rapprochée d'un invité en prend le type chez l'hôte, et le dit ;
// l'hôte dit son nœud, sa grappe et ses invités.
func TestPlacement_AGuestTakesItsKindFromItsHost(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("put: %d %s", got.Code, got.Body.String())
	}
	var response machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+dockerVMID, nil), &response)
	if response.Kind != "vm" || response.Host == nil || response.Host.Source != "guest" || response.Host.MachineID != pveID || response.Host.Node != "pve" {
		t.Fatalf("kind %q host %+v", response.Kind, response.Host)
	}
	var host machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+pveID, nil), &host)
	if host.Proxmox.Node != "pve" || host.Proxmox.Cluster == nil || host.Proxmox.Cluster.Name != "homelab" || len(host.Proxmox.GuestList) != 2 {
		t.Fatalf("l'hôte ne dit pas ses invités : %+v", host.Proxmox)
	}
	if host.Host != nil {
		t.Fatalf("un hôte n'est pas porté par ses invités : %+v", host.Host)
	}
}

// La VM est renommée chez l'hôte : son nom ne dit plus rien, son adresse MAC
// la retrouve. C'est tout l'intérêt de la clé la plus forte.
func TestPlacement_ARenamedGuestIsFoundByItsMAC(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	api.setGuestName("renamed-by-someone")
	token := openAgentSession(t, server, dockerVMID)
	report := hostinfo.Report{Virt: "kvm", Interfaces: []hostinfo.Interface{{Name: "eth0", MAC: strings.ToLower(pveGuestMAC)}}}
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{Host: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("put: %d %s", got.Code, got.Body.String())
	}
	var response machineJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/machines/"+dockerVMID, nil), &response)
	if response.Host == nil || response.Host.MachineID != pveID || response.Proxmox.Hosted == nil || response.Proxmox.Hosted.VMID != 5012 {
		t.Fatalf("la VM renommée n'est pas retrouvée : %+v", response)
	}
}
