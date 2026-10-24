package proxmox

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

func validDefinition() Definition {
	return Definition{APIURL: "https://pve.lan:8006", TokenID: testTokenID, TokenSecret: testTokenSecret}
}

func TestDefinition_NormalizeKeepsAValidInputAndPutsItInShape(t *testing.T) {
	pin := strings.Repeat("AB", 32)
	colons := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	for name, tc := range map[string]struct{ in, want string }{
		"trailing slash": {"https://pve.lan:8006/", "https://pve.lan:8006"},
		"spaces":         {"  https://pve.lan:8006  ", "https://pve.lan:8006"},
		"no port":        {"https://10.10.11.2", "https://10.10.11.2"},
		"ipv6":           {"https://[fd00::2]:8006", "https://[fd00::2]:8006"},
		"uppercase host": {"https://PVE.lan:8006", "https://PVE.lan:8006"},
	} {
		definition := validDefinition()
		definition.APIURL = tc.in
		got, err := definition.Normalize()
		if err != nil || got.APIURL != tc.want {
			t.Errorf("%s: %q, %v", name, got.APIURL, err)
		}
	}
	for name, raw := range map[string]string{"bare hex": pin, "openssl colons": colons, "sha256 prefix": "SHA256:" + colons} {
		definition := validDefinition()
		definition.Pin = raw
		got, err := definition.Normalize()
		if err != nil || got.Pin != strings.Repeat("ab", 32) {
			t.Errorf("pin %s: %q, %v", name, got.Pin, err)
		}
	}
}

func TestDefinition_NormalizeRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*Definition)
		want   error
	}{
		"empty address":       {func(d *Definition) { d.APIURL = "" }, ErrURLInvalid},
		"http":                {func(d *Definition) { d.APIURL = "http://pve.lan:8006" }, ErrURLInvalid},
		"a path":              {func(d *Definition) { d.APIURL = "https://pve.lan:8006/api2/json" }, ErrURLInvalid},
		"credentials":         {func(d *Definition) { d.APIURL = "https://root:pw@pve.lan:8006" }, ErrURLInvalid},
		"a query":             {func(d *Definition) { d.APIURL = "https://pve.lan:8006?x=1" }, ErrURLInvalid},
		"no host":             {func(d *Definition) { d.APIURL = "https://:8006" }, ErrURLInvalid},
		"too long":            {func(d *Definition) { d.APIURL = "https://" + strings.Repeat("a", 300) + ".lan" }, ErrURLInvalid},
		"token without !":     {func(d *Definition) { d.TokenID = "lecteur@pve" }, ErrTokenIDInvalid},
		"token without @":     {func(d *Definition) { d.TokenID = "lecteur!lecture" }, ErrTokenIDInvalid},
		"token with space":    {func(d *Definition) { d.TokenID = "lec teur@pve!x" }, ErrTokenIDInvalid},
		"secret too short":    {func(d *Definition) { d.TokenSecret = "short" }, ErrSecretInvalid},
		"secret with space":   {func(d *Definition) { d.TokenSecret = "aaaa bbbb cccc" }, ErrSecretInvalid},
		"secret with newline": {func(d *Definition) { d.TokenSecret = "aaaaaaaa\nbbbb" }, ErrSecretInvalid},
		"secret too long":     {func(d *Definition) { d.TokenSecret = strings.Repeat("a", 201) }, ErrSecretInvalid},
		"pin not hex":         {func(d *Definition) { d.Pin = strings.Repeat("zz", 32) }, ErrPinInvalid},
		"pin too short":       {func(d *Definition) { d.Pin = "abcd" }, ErrPinInvalid},
	} {
		definition := validDefinition()
		tc.change(&definition)
		if _, err := definition.Normalize(); err == nil || err.Error() != tc.want.Error() {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestHostNode(t *testing.T) {
	one := Reading{Guests: []Guest{{Node: "pve"}}, Storages: []Storage{{Node: "pve"}}}
	if got := hostNode(one, "anything"); got != "pve" {
		t.Errorf("a single node is the host: %q", got)
	}
	cluster := Reading{Guests: []Guest{{Node: "pve1"}, {Node: "pve2"}}, Storages: []Storage{{Node: "pve1"}}}
	if got := hostNode(cluster, "PVE2"); got != "pve2" {
		t.Errorf("the node named like the hostname: %q", got)
	}
	if got := hostNode(cluster, "elsewhere"); got != "" {
		t.Errorf("an API that does not say which node answers must leave the node empty: %q", got)
	}
	if got := hostNode(Reading{}, "pve"); got != "" {
		t.Errorf("nothing read: %q", got)
	}
}

func status(id, hostname string) machine.Status {
	return machine.Status{Machine: machine.Machine{ID: id, Name: id, Hostname: hostname}}
}

func TestLink_ByHostnameExactThenIgnoringCase(t *testing.T) {
	guests := []Guest{{VMID: 1, Name: "docker1"}, {VMID: 2, Name: "DNS"}, {VMID: 3, Name: "nobody"}, {VMID: 4, Name: ""}, {VMID: 5, Name: "pve"}}
	machines := []machine.Status{status("host", "pve"), status("a", "docker1"), status("b", "dns"), status("c", "Docker1")}
	link(guests, machines, "host")
	want := map[int]string{1: "a", 2: "b", 3: "", 4: "", 5: ""}
	for _, guest := range guests {
		if guest.LinkedMachineID != want[guest.VMID] {
			t.Errorf("vm %d (%s): linked to %q, want %q", guest.VMID, guest.Name, guest.LinkedMachineID, want[guest.VMID])
		}
	}
}

func TestLink_AnAmbiguousCaseInsensitiveMatchLinksNothing(t *testing.T) {
	guests := []Guest{{VMID: 1, Name: "web"}}
	link(guests, []machine.Status{status("a", "WEB"), status("b", "Web")}, "host")
	if guests[0].LinkedMachineID != "" {
		t.Errorf("two candidates differ only by case, nothing may be guessed: %q", guests[0].LinkedMachineID)
	}
}

func TestStorage_AcceptsBackups(t *testing.T) {
	if !(Storage{Content: []string{"iso", "backup"}}).AcceptsBackups() || (Storage{Content: []string{"images"}}).AcceptsBackups() {
		t.Fatal("AcceptsBackups wrong")
	}
}
