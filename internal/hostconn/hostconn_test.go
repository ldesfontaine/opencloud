package hostconn

import (
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReport_Validate(t *testing.T) {
	good := Report{Available: true, Connections: []Connection{
		{Port: 3000, Origin: OriginAccount, Name: "forgejo"},
		{Port: 3000, Origin: OriginAddress, Name: "192.168.1.20"},
		{Port: 443, Origin: OriginInternet},
	}}
	if err := good.Validate(); err != nil {
		t.Fatalf("good report refused: %v", err)
	}
	if err := (Report{Reason: ReasonUnreadable}).Validate(); err != nil {
		t.Fatalf("unreadable report refused: %v", err)
	}
	bad := map[string]Report{
		"port zero":                {Available: true, Connections: []Connection{{Port: 0, Origin: OriginInternet}}},
		"port too high":            {Available: true, Connections: []Connection{{Port: 65536, Origin: OriginInternet}}},
		"unknown origin":           {Available: true, Connections: []Connection{{Port: 80, Origin: "process", Name: "nginx"}}},
		"account with a space":     {Available: true, Connections: []Connection{{Port: 80, Origin: OriginAccount, Name: "Jean Dupont"}}},
		"account too long":         {Available: true, Connections: []Connection{{Port: 80, Origin: OriginAccount, Name: strings.Repeat("a", 33)}}},
		"public address":           {Available: true, Connections: []Connection{{Port: 80, Origin: OriginAddress, Name: "8.8.8.8"}}},
		"loopback as address":      {Available: true, Connections: []Connection{{Port: 80, Origin: OriginAddress, Name: "127.0.0.1"}}},
		"internet with an address": {Available: true, Connections: []Connection{{Port: 80, Origin: OriginInternet, Name: "8.8.8.8"}}},
		"address with a zone":      {Available: true, Connections: []Connection{{Port: 80, Origin: OriginAddress, Name: "fe80::1%eth0"}}},
		"duplicate":                {Available: true, Connections: []Connection{{Port: 80, Origin: OriginInternet}, {Port: 80, Origin: OriginInternet}}},
		"unavailable with rows":    {Reason: ReasonUnreadable, Connections: []Connection{{Port: 80, Origin: OriginInternet}}},
		"unavailable no reason":    {},
		"reason while available":   {Available: true, Reason: ReasonUnreadable},
	}
	for name, report := range bad {
		if report.Validate() == nil {
			t.Errorf("%s: report accepted", name)
		}
	}
	tooMany := Report{Available: true}
	for port := 1; port <= MaxConnections+1; port++ {
		tooMany.Connections = append(tooMany.Connections, Connection{Port: port, Origin: OriginInternet})
	}
	if tooMany.Validate() == nil {
		t.Error("report over the bound accepted")
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"3600007F:0035":                         "127.0.0.54:53",
		"0A01A8C0:0016":                         "192.168.1.10:22",
		"00000000000000000000000001000000:01BB": "[::1]:443",
		"0000000000000000FFFF00000100007F:0050": "127.0.0.1:80",
	}
	for text, want := range cases {
		got, ok := parseEndpoint(text)
		if !ok || got.String() != want {
			t.Errorf("parseEndpoint(%q) = %v, %v; want %s", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "0100007F", "ZZZZ:0050", "0100:0050", "0100007F:XYZ"} {
		if _, ok := parseEndpoint(text); ok {
			t.Errorf("parseEndpoint(%q) accepted", text)
		}
	}
}

const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

// writeProc écrit une fausse table de l'hôte : les lignes de tcp et de tcp6.
func writeProc(t *testing.T, tcp, tcp6 string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tcp"), []byte(header+tcp), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tcp6"), []byte(header+tcp6), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fake(procNet string) Source {
	return Source{ProcNet: procNet, Own: []netip.Addr{netip.MustParseAddr("192.168.1.10")}, AccountName: func(uid int) string {
		return map[int]string{1001: "forgejo", 0: "root"}[uid]
	}}
}

func TestRead_KeepsWhoJoinsWhichPort(t *testing.T) {
	tcp := `   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000   990        0 11 1 0 100 0 0 10 0
   1: 0A01A8C0:0BB8 1401A8C0:D431 01 00000000:00000000 00:00000000 00000000   990        0 12 1 0 100 0 0 10 0
   2: 0A01A8C0:0BB8 08080808:9C40 01 00000000:00000000 00:00000000 00000000   990        0 13 1 0 100 0 0 10 0
   3: 0A01A8C0:0BB8 09090909:9C41 01 00000000:00000000 00:00000000 00000000   990        0 14 1 0 100 0 0 10 0
   4: 0100007F:D432 0100007F:0BB8 01 00000000:00000000 00:00000000 00000000  1001        0 15 1 0 100 0 0 10 0
   5: 0100007F:0BB8 0100007F:D432 01 00000000:00000000 00:00000000 00000000   990        0 16 1 0 100 0 0 10 0
   6: 0A01A8C0:D433 0A01A8C0:0BB8 01 00000000:00000000 00:00000000 00000000     0        0 17 1 0 100 0 0 10 0
   7: 0A01A8C0:D434 0A02A8C0:1538 01 00000000:00000000 00:00000000 00000000  1001        0 18 1 0 100 0 0 10 0
   8: 0A01A8C0:0BB8 1401A8C0:D435 06 00000000:00000000 00:00000000 00000000   990        0 19 1 0 100 0 0 10 0
`
	report := Read(fake(writeProc(t, tcp, "")))
	want := []Connection{
		{Port: 3000, Origin: OriginAccount, Name: "forgejo"},
		{Port: 3000, Origin: OriginAccount, Name: "root"},
		{Port: 3000, Origin: OriginAddress, Name: "192.168.1.20"},
		{Port: 3000, Origin: OriginInternet},
	}
	if !report.Available || len(report.Connections) != len(want) {
		t.Fatalf("report = %+v, want %v", report, want)
	}
	for index, connection := range want {
		if report.Connections[index] != connection {
			t.Errorf("connection %d = %+v, want %+v", index, report.Connections[index], connection)
		}
	}
	if err := report.Validate(); err != nil {
		t.Errorf("what the agent reads, the server accepts: %v", err)
	}
}

func TestRead_UnknownAccountGetsANumberedName(t *testing.T) {
	tcp := `   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000   990        0 11 1 0 100 0 0 10 0
   1: 0100007F:D432 0100007F:0BB8 01 00000000:00000000 00:00000000 00000000  4242        0 15 1 0 100 0 0 10 0
`
	report := Read(fake(writeProc(t, tcp, "")))
	if len(report.Connections) != 1 || report.Connections[0].Name != "uid-4242" {
		t.Fatalf("connections = %+v", report.Connections)
	}
}

func TestRead_ReadsIPv6AndTruncates(t *testing.T) {
	tcp6 := `   0: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 21 1 0 100 0 0 10 0
   1: 0000000000000000FFFF00000A01A8C0:0050 0000000000000000FFFF00000A02A8C0:C350 01 00000000:00000000 00:00000000 00000000     0        0 22 1 0 100 0 0 10 0
   2: 00000000000000000000000001000000:0050 000000000000000000000000FEFEFEFE:C351 01 00000000:00000000 00:00000000 00000000     0        0 23 1 0 100 0 0 10 0
`
	report := Read(fake(writeProc(t, "", tcp6)))
	if len(report.Connections) != 2 || report.Connections[0] != (Connection{Port: 80, Origin: OriginAddress, Name: "192.168.2.10"}) || report.Connections[1].Origin != OriginInternet {
		t.Fatalf("connections = %+v", report.Connections)
	}
}

func TestRead_MissingTableIsUnreadable(t *testing.T) {
	report := Read(Source{ProcNet: filepath.Join(t.TempDir(), "absent")})
	if report.Available || report.Reason != ReasonUnreadable || report.Validate() != nil {
		t.Fatalf("report = %+v", report)
	}
}

func TestRead_BadLinesAreSkipped(t *testing.T) {
	tcp := "garbage\n1 2 3\n   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000   990        0 11 1 0 100 0 0 10 0\n"
	if report := Read(fake(writeProc(t, tcp, ""))); !report.Available || len(report.Connections) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

// Une mesure, pas une simulation : la vraie table de cette machine, avec une
// vraie connexion de bouclage. Sans /proc/net/tcp (hors Linux), le test saute.
func TestRead_RealLoopbackConnection(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("pas de /proc/net/tcp")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip("pas de bouclage IPv4")
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	client, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer (<-accepted).Close()

	port := listener.Addr().(*net.TCPAddr).Port
	report := Read(Local())
	for _, connection := range report.Connections {
		if connection.Port == port && connection.Origin == OriginAccount {
			if err := report.Validate(); err != nil {
				t.Fatalf("la vraie table donne un rapport refusé : %v", err)
			}
			return
		}
	}
	t.Fatalf("la connexion de bouclage vers le port %d n'est pas vue : %+v", port, report)
}

// Banc de non-fuite, côté agent : la table du noyau et la fiche du compte
// portent des valeurs que le rapport ne doit jamais emporter (inode, nom
// complet, dossier personnel, adresse publique d'un visiteur).
func TestRead_NoSeededValueLeavesTheAgent(t *testing.T) {
	seeds := []string{"hunter2-Zq83", "tok_9f3a77c1d2e8", "9.9.9.9", "8.8.8.8", "40000", "99887766"}
	tcp := `   0: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000   990        0 99887766 1 hunter2-Zq83 100 0 0 10 0
   1: 0A01A8C0:0BB8 08080808:9C40 01 00000000:00000000 00:00000000 00000000   990        0 99887766 1 tok_9f3a77c1d2e8 100 0 0 10 0
   2: 0100007F:9C41 0100007F:0BB8 01 00000000:00000000 00:00000000 00000000  1001        0 99887766 1 0 100 0 0 10 0
   3: 0A01A8C0:0BB8 09090909:9C42 01 00000000:00000000 00:00000000 00000000   990        0 99887766 1 0 100 0 0 10 0
`
	source := fake(writeProc(t, tcp, ""))
	source.AccountName = func(int) string { return "Hunter2-Zq83 /home/tok_9f3a77c1d2e8" }
	report := Read(source)
	encoded := strings.ToLower(string(mustMarshal(t, report)))
	for _, seed := range seeds {
		if strings.Contains(encoded, strings.ToLower(seed)) {
			t.Errorf("%q est dans le rapport : %s", seed, encoded)
		}
	}
	if len(report.Connections) == 0 {
		t.Fatal("le rapport devait porter les connexions")
	}
}

func mustMarshal(t *testing.T, report Report) []byte {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
