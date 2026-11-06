package server

import (
	"context"
	"net/http"
	"testing"
)

func networkMapOf(t *testing.T, server *testServer) networkMapJSON {
	t.Helper()
	got := callAPI(server.Server, http.MethodGet, "/api/network", nil)
	if got.Code != http.StatusOK {
		t.Fatalf("carte : %d %s", got.Code, got.Body.String())
	}
	var result networkMapJSON
	decodeAPI(t, got, &result)
	return result
}

func mapLinkByID(result networkMapJSON, id string) *mapLinkJSON {
	for index := range result.Links {
		if result.Links[index].ID == id {
			return &result.Links[index]
		}
	}
	return nil
}

// mapBoxes aplatit les couloirs de la maison.
func mapBoxes(result networkMapJSON) []mapBoxJSON {
	var boxes []mapBoxJSON
	for _, lane := range result.Home {
		boxes = append(boxes, lane.Boxes...)
	}
	return boxes
}

// Un hôte Proxmox lu : chaque invité a sa carte dans le couloir de sa zone, le
// tag VLAN que l'API a dit de sa carte, et nomme son hôte ; l'hôte dit combien
// il en porte. La VM rapprochée d'une machine enrôlée porte son numéro,
// l'invité sans agent le sien.
func TestAPI_NetworkMap_GuestsHaveTheirCardInTheZoneLaneAndNameTheirHost(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("proxmox : %d %s", got.Code, got.Body.String())
	}
	atHome(t, server, pveID, "10.8.0.20")
	atHome(t, server, dockerVMID, "10.8.0.21")

	result := networkMapOf(t, server)

	var host, container, vm *mapBoxJSON
	boxes := mapBoxes(result)
	for index := range boxes {
		switch {
		case boxes[index].MachineID == pveID:
			host = &boxes[index]
		case boxes[index].VMID == 200:
			container = &boxes[index]
		case boxes[index].MachineID == dockerVMID:
			vm = &boxes[index]
		}
	}
	if host == nil || host.GuestCount != 2 || host.HostID != "" {
		t.Fatalf("l'hôte porte ses deux invités : %+v", host)
	}
	if container == nil || container.HostID != host.ID || container.Kind != "lxc" || container.Managed || container.GuestStatus != "stopped" {
		t.Errorf("le conteneur sans agent : %+v", container)
	}
	if vm == nil || vm.HostID != host.ID || vm.VMID != 5012 || vm.Kind != "vm" || !vm.Managed {
		t.Errorf("la VM rapprochée : %+v", vm)
	}
	zone := mapZoneByID(result, container.ZoneID)
	if zone == nil || zone.VLAN != 50 || zone.Name != "VLAN 50" {
		t.Errorf("la zone vient du tag VLAN : %+v", result.Zones)
	}
	for _, lane := range result.Home {
		for _, box := range lane.Boxes {
			if box.ZoneID != lane.ZoneID {
				t.Errorf("%s est dans le couloir %q, sa zone est %q", box.Name, lane.ZoneID, box.ZoneID)
			}
		}
	}
}

// atHome donne à une machine du banc une adresse privée : l'enrôlement du banc
// la dit publique.
func atHome(t *testing.T, server *testServer, machineID, address string) {
	t.Helper()
	found, err := server.machines.Get(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := server.machines.Connect(context.Background(), found.Machine, address, "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.machines.Disconnect(session) })
}

// Un hôte sans hôte connu dont l'adresse est publique est ailleurs, avec ses
// invités, quel que soit le type que l'agent a détecté.
func TestAPI_NetworkMap_PublicHostIsElsewhereWithItsGuests(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("proxmox : %d %s", got.Code, got.Body.String())
	}

	result := networkMapOf(t, server)

	if len(result.Outside) != 3 {
		t.Fatalf("l'hôte et ses deux invités sont ailleurs : %+v", result.Outside)
	}
	for _, lane := range result.Home {
		for _, box := range lane.Boxes {
			if box.MachineID == pveID || box.HostID != "" {
				t.Errorf("%s est chez vous : %+v", box.Name, box)
			}
		}
	}
}

func mapZoneByID(result networkMapJSON, id string) *mapZoneJSON {
	for index := range result.Zones {
		if result.Zones[index].ID == id {
			return &result.Zones[index]
		}
	}
	return nil
}
