package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

func TestNICOf(t *testing.T) {
	cases := map[string]NIC{
		"virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,firewall=1":                  {MAC: "bc:24:11:aa:bb:cc", Bridge: "vmbr0"},
		"e1000=bc:24:11:00:00:01,bridge=vmbr1,tag=20":                       {MAC: "bc:24:11:00:00:01", Bridge: "vmbr1", VLAN: 20},
		"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:AA:BB:DD,ip=dhcp,type=veth": {MAC: "bc:24:11:aa:bb:dd", Bridge: "vmbr0"},
		"bridge=vmbr0,firewall=1":                                           {Bridge: "vmbr0"},
		"virtio=pas-une-mac":                                                {},
		"virtio=bc:24:11:00:00:02,tag=9999":                                 {MAC: "bc:24:11:00:00:02"},
		"virtio=bc:24:11:00:00:03,tag=abc":                                  {MAC: "bc:24:11:00:00:03"},
	}
	for nic, want := range cases {
		if got := nicOf(nic); got != want {
			t.Errorf("%q : got %+v, want %+v", nic, got, want)
		}
	}
}

// Seuls la MAC, le pont et le tag survivent : ni le pare-feu de la carte, ni
// son adresse IP, ni un champ inconnu (ici, une valeur secrète semée).
func TestNICOf_KeepsNothingElse(t *testing.T) {
	got := nicOf("virtio=BC:24:11:AA:BB:CC,bridge=vmbr0,tag=50,ip=10.1.2.3/24,gw=10.1.2.1,note=SECRET-SEMEE")
	if rendered := fmt.Sprintf("%+v", got); strings.Contains(rendered, "SECRET") || strings.Contains(rendered, "10.1.2") {
		t.Errorf("la carte garde plus que MAC, pont et tag : %s", rendered)
	}
	if got.VLAN != 50 || got.Bridge != "vmbr0" {
		t.Errorf("carte : %+v", got)
	}
}

// configHandler sert la configuration des invités et l'état de la grappe.
func configHandler(configs map[string]string, clusterBody string) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.TrimPrefix(r.URL.Path, "/api2/json")
		if path == "/cluster/status" && clusterBody != "" {
			_, _ = w.Write([]byte(clusterBody))
			return true
		}
		if body, ok := configs[path]; ok {
			_, _ = w.Write([]byte(body))
			return true
		}
		return false
	}
}

func TestClient_Read_GuestMACsAndClusterName(t *testing.T) {
	api := newFakeAPI(t)
	api.handler = configHandler(map[string]string{
		"/nodes/pve/qemu/5012/config": `{"data":{"name":"docker1","net0":"virtio=BC:24:11:AA:BB:CC,bridge=vmbr0","net1":"e1000=BC:24:11:AA:BB:CD,bridge=vmbr1","memory":2048}}`,
		"/nodes/pve/lxc/200/config":   `{"data":{"hostname":"dns","net0":"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:AA:BB:DD,ip=dhcp"}}`,
	}, `{"data":[{"type":"cluster","name":"homelab","nodes":2,"quorate":1},{"type":"node","name":"pve","online":1}]}`)

	reading, err := NewClient(api.credentials(api.pin())).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Cluster != "homelab" {
		t.Errorf("cluster %q", reading.Cluster)
	}
	byVMID := map[int]Guest{}
	for _, guest := range reading.Guests {
		byVMID[guest.VMID] = guest
	}
	docker := byVMID[5012].MACs
	if len(docker) != 2 || !containsAll(docker, "bc:24:11:aa:bb:cc", "bc:24:11:aa:bb:cd") {
		t.Errorf("docker1 : %v", docker)
	}
	if !reflect.DeepEqual(byVMID[200].MACs, []string{"bc:24:11:aa:bb:dd"}) {
		t.Errorf("dns : %v", byVMID[200].MACs)
	}
	if len(byVMID[9000].MACs) != 0 {
		t.Errorf("a guest whose configuration is unreadable keeps no address: %v", byVMID[9000].MACs)
	}
}

func containsAll(values []string, wanted ...string) bool {
	for _, want := range wanted {
		found := false
		for _, value := range values {
			found = found || value == want
		}
		if !found {
			return false
		}
	}
	return true
}

func TestClient_Read_ASingleNodeHasNoCluster(t *testing.T) {
	api := newFakeAPI(t)
	api.handler = configHandler(nil, `{"data":[{"type":"node","name":"pve","online":1,"local":1}]}`)
	reading, err := NewClient(api.credentials(api.pin())).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Cluster != "" {
		t.Errorf("cluster %q", reading.Cluster)
	}
}

// Un jeton sans le droit de lire les configurations ne fait pas échouer le
// relevé, et n'est pas interrogé invité après invité.
func TestClient_Read_ATokenThatCannotReadConfigsStopsAskingAndKeepsTheReading(t *testing.T) {
	api := newFakeAPI(t)
	api.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/config") || r.URL.Path == "/api2/json/cluster/status" {
			http.Error(w, `{"data":null}`, http.StatusForbidden)
			return true
		}
		return false
	}
	reading, err := NewClient(api.credentials(api.pin())).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reading.Guests) != 3 || reading.Cluster != "" {
		t.Fatalf("la lecture est gardée : %+v", reading)
	}
	configReads := 0
	for _, path := range api.requested() {
		if strings.HasSuffix(path, "/config") {
			configReads++
		}
	}
	if configReads != 1 {
		t.Errorf("%d lectures de configuration après un refus, une attendue", configReads)
	}
}

func TestClient_EscapesTheNodeInTheConfigPath(t *testing.T) {
	api := newFakeAPI(t)
	if _, err := NewClient(api.credentials(api.pin())).GuestNICs(context.Background(), "../x", KindQEMU, 100); err == nil {
		t.Fatal("un chemin qui n'existe pas doit échouer")
	}
	for _, path := range api.requested() {
		if strings.Contains(path, "/x/") && !strings.Contains(path, "%2F") {
			t.Errorf("le nœud n'est pas échappé : %s", path)
		}
	}
}

func withMACs(base machine.Status, macs ...string) machine.Status {
	base.MACs = macs
	return base
}

func TestLink_TheMACBeatsTheName(t *testing.T) {
	guests := []Guest{{VMID: 1, Name: "web", MACs: []string{"bc:24:11:00:00:01"}}}
	machines := []machine.Status{
		status("by-name", "web"),
		withMACs(status("by-mac", "renamed"), "bc:24:11:00:00:01"),
	}
	link(guests, machines, "host")
	if guests[0].LinkedMachineID != "by-mac" {
		t.Errorf("linked to %q: the address is stronger than the name", guests[0].LinkedMachineID)
	}
}

func TestLink_TwoGuestsWithTheSameNameAreToldApartByTheirAddress(t *testing.T) {
	guests := []Guest{
		{VMID: 1, Name: "web", MACs: []string{"bc:24:11:00:00:01"}},
		{VMID: 2, Name: "web", MACs: []string{"bc:24:11:00:00:02"}},
	}
	machines := []machine.Status{
		withMACs(status("first", "web"), "bc:24:11:00:00:01"),
		withMACs(status("second", "web"), "bc:24:11:00:00:02"),
	}
	link(guests, machines, "host")
	if guests[0].LinkedMachineID != "first" || guests[1].LinkedMachineID != "second" {
		t.Errorf("linked to %q and %q", guests[0].LinkedMachineID, guests[1].LinkedMachineID)
	}
}

func TestLink_ACopiedAddressProvesNothingAndFallsBackToTheName(t *testing.T) {
	guests := []Guest{{VMID: 1, Name: "web", MACs: []string{"bc:24:11:00:00:01"}}}
	machines := []machine.Status{
		withMACs(status("a", "web"), "bc:24:11:00:00:01"),
		withMACs(status("b", "clone"), "bc:24:11:00:00:01"),
	}
	link(guests, machines, "host")
	if guests[0].LinkedMachineID != "a" {
		t.Errorf("linked to %q: ambiguous address, the name decides", guests[0].LinkedMachineID)
	}
}

func TestLink_AMachinePinnedToAnotherHostIsNotThisHostsGuest(t *testing.T) {
	guests := []Guest{{VMID: 1, Name: "web", MACs: []string{"bc:24:11:00:00:01"}}}
	pinned := withMACs(status("web", "web"), "bc:24:11:00:00:01")
	pinned.HostOverride = "other-host"
	link(guests, []machine.Status{pinned}, "host")
	if guests[0].LinkedMachineID != "" {
		t.Errorf("linked to %q: the operator put it elsewhere", guests[0].LinkedMachineID)
	}
	pinned.HostOverride = "host"
	link(guests, []machine.Status{pinned}, "host")
	if guests[0].LinkedMachineID != "web" {
		t.Errorf("a machine pinned to this very host stays a candidate: %q", guests[0].LinkedMachineID)
	}
}
