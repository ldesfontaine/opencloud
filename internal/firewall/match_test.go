package firewall_test

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/firewall"
)

func TestMatchMachine(t *testing.T) {
	machines := []firewall.KnownMachine{
		{ID: "a", Name: "nas", Host: "192.168.1.20", MACs: []string{"aa:bb:cc:dd:ee:ff"}},
		{ID: "b", Name: "web", Host: "192.168.1.21", MACs: []string{"11:22:33:44:55:66"}},
		{ID: "c", Name: "twin-1", Host: "192.168.1.50"},
		{ID: "d", Name: "twin-2", Host: "192.168.1.50"},
	}
	cases := map[string]struct {
		lease firewall.Lease
		want  string
	}{
		"by address":                 {firewall.Lease{Address: "192.168.1.20"}, "a"},
		"mac wins over the address":  {firewall.Lease{Address: "192.168.1.20", MAC: "11:22:33:44:55:66"}, "b"},
		"mac in capitals":            {firewall.Lease{MAC: "AA:BB:CC:DD:EE:FF"}, "a"},
		"nobody":                     {firewall.Lease{Address: "192.168.1.99", MAC: "00:00:00:00:00:01"}, ""},
		"two machines, same address": {firewall.Lease{Address: "192.168.1.50"}, ""},
		"empty lease":                {firewall.Lease{}, ""},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			found, ok := firewall.MatchMachine(test.lease, machines)
			if (test.want == "") == ok || found.ID != test.want {
				t.Fatalf("got %q (%v), want %q", found.ID, ok, test.want)
			}
		})
	}
}
