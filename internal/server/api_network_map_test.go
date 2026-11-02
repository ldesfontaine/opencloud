package server

import (
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

// Un hôte Proxmox lu : ses invités sont dans sa boîte, rangés par le tag VLAN
// que l'API a dit de leur carte, jamais à côté de lui ; la VM rapprochée d'une
// machine enrôlée porte son numéro, l'invité sans agent le sien.
func TestAPI_NetworkMap_GuestsAreInsideTheirHostGroupedByVLAN(t *testing.T) {
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("proxmox : %d %s", got.Code, got.Body.String())
	}

	result := networkMapOf(t, server)

	var host *mapBoxJSON
	for index := range result.Home {
		if result.Home[index].MachineID == pveID {
			host = &result.Home[index]
		}
		if result.Home[index].MachineID == dockerVMID || result.Home[index].VMID == 200 {
			t.Errorf("un invité apparaît à côté de son hôte : %+v", result.Home[index])
		}
	}
	if host == nil || len(host.Groups) != 1 || len(host.Groups[0].Guests) != 2 {
		t.Fatalf("l'hôte porte ses deux invités dans une zone : %+v", host)
	}
	guests := host.Groups[0].Guests
	if guests[0].VMID != 200 || guests[0].Kind != "lxc" || guests[0].Managed || guests[0].GuestStatus != "stopped" {
		t.Errorf("le conteneur sans agent : %+v", guests[0])
	}
	if guests[1].MachineID != dockerVMID || guests[1].VMID != 5012 || guests[1].Kind != "vm" || !guests[1].Managed {
		t.Errorf("la VM rapprochée : %+v", guests[1])
	}
	zone := mapZoneByID(result, host.Groups[0].ZoneID)
	if zone == nil || zone.VLAN != 50 || zone.Name != "VLAN 50" {
		t.Errorf("la zone vient du tag VLAN : %+v", result.Zones)
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
