package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

// e2eAddrEnv porte l'adresse d'écoute du serveur du parcours navigateur
// (web/e2e). Sans elle, le test passe son tour : seul `make e2e` le lance.
const e2eAddrEnv = "OPENCLOUD_E2E_ADDR"

// TestE2EFixtureServer sert le vrai serveur sur des données fictives, celles
// des bancs de test, jusqu'au signal d'arrêt. Le parcours navigateur
// (Playwright) s'y branche : même base, mêmes composants, horloge figée, rien
// de réel. Les identifiants de l'opérateur sont posés sur chaque requête.
func TestE2EFixtureServer(t *testing.T) {
	addr := os.Getenv(e2eAddrEnv)
	if addr == "" {
		t.Skip(e2eAddrEnv + " n'est pas posée : serveur du parcours navigateur non demandé")
	}
	servertest.RequireFront(t)
	server := newTestServer(t)
	seedE2E(t, server)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.handler}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()
	t.Logf("serveur de parcours prêt sur %s", listener.Addr())

	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	<-stop.Done()
	_ = httpServer.Close()
}

// seedE2E remplit chaque écran : une machine distante vue à l'instant, ses
// conteneurs et ses mesures, l'annuaire, la page de statut, les tâches, les
// sondes, les alertes, les ordres et le journal.
func seedE2E(t *testing.T, ts *testServer) {
	t.Helper()
	ts.seedAll(t)
	ts.seedResources(t)
	ts.seedProposals(t)
	ts.seedAlerts(t)
	ts.seedActions(t)
	ts.seedAudit(t)
	ts.signaledAt(t, remoteID, testNow)
	seedE2EBackups(t, ts)
	seedE2EFirewall(t, ts)
	seedE2EApply(t, ts)
	seedE2EVirtualized(t, ts)
	seedE2EEdge(t, ts)
	seedE2ESystemd(t, ts)
	seedE2EProxmox(t, ts)
	seedE2ELinks(t, ts)
}

// seedE2EVirtualized fait rapporter « machine virtuelle » aux deux machines,
// sans hôte connu : la distante, jointe par une adresse publique, est un VPS ;
// la machine openCloud, sur un réseau privé, une VM.
func seedE2EVirtualized(t *testing.T, ts *testServer) {
	t.Helper()
	for _, id := range []string{remoteID, machine.LocalID} {
		report := hostinfo.Report{}
		if id == remoteID {
			report.Interfaces = []hostinfo.Interface{{Name: "eth0", MAC: e2eRemoteMAC}}
		}
		if _, err := ts.db.ReplaceHostReport(context.Background(), id, machine.KindVM, report); err != nil {
			t.Fatal(err)
		}
	}
}

// seedE2EBackups pose la clé, une destination sur la machine distante et le
// plan de la base : l'écran Sauvegardes et l'assistant de plan en ont besoin.
func seedE2EBackups(t *testing.T, ts *testServer) {
	t.Helper()
	for _, name := range []string{brand.SecretKeyFile, brand.ServerKeyFile} {
		if err := ts.state.WriteFile(name, []byte("une clé de test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := ts.machines.Get(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := ts.machines.Connect(context.Background(), remote.Machine, "192.0.2.1", "v0.0.1", "backup.receive=/srv/backups,backup.produce=proxmox-vzdump", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := callAPI(ts.Server, http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase}); got.Code != http.StatusCreated {
		t.Fatalf("keyring: %d %s", got.Code, got.Body.String())
	}
	created := callAPI(ts.Server, http.MethodPost, "/api/backups/destinations", map[string]any{"name": "store-1", "machine_id": remoteID, "path": "/srv/backups"})
	if created.Code != http.StatusCreated {
		t.Fatalf("destination: %d %s", created.Code, created.Body.String())
	}
	var destination backupDestinationJSON
	decodeAPI(t, created, &destination)
	if got := callAPI(ts.Server, http.MethodPut, "/api/backups/plans/database", map[string]any{"source_kind": "database", "destination_id": destination.ID, "keep": 14}); got.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", got.Code, got.Body.String())
	}
	// L'agent a rapporté ses droits puis s'est tu : sans flux ouvert, la machine
	// est hors ligne, comme sur une installation dont l'agent est arrêté.
	ts.machines.Disconnect(session)
}

// seedE2EFirewall déclare un pare-feu OPNsense sur le faux serveur des bancs.
func seedE2EFirewall(t *testing.T, ts *testServer) {
	t.Helper()
	fake := opnsensetest.New(t)
	fake.Answer(opnsensetest.InterfacesPath, http.StatusOK, e2eFirewallInterfaces)
	fake.Answer(opnsensetest.RulesPath, http.StatusOK, e2eFirewallRules())
	fake.Answer(opnsensetest.DnsmasqLeasesPath, http.StatusOK, e2eFirewallLeases)
	declareFirewallThroughAPI(t, ts, fake)
}

// e2eFirewallInterfaces : quatre zones, dont une sans lien, pour que la fiche
// montre des cartes de chaque état.
const e2eFirewallInterfaces = `{"total":4,"rowCount":4,"current":1,"rows":[
	{"identifier":"wan","device":"igb0","description":"WAN","status":"up","addr4":"203.0.113.2/24"},
	{"identifier":"lan","device":"igb1","description":"LAN","status":"up","addr4":"192.168.1.1/24"},
	{"identifier":"opt1","device":"igb2","description":"DMZ","status":"down","addr4":"10.0.20.1/24"},
	{"identifier":"opt2","device":"igb3","description":"IA","status":"no carrier","addr4":"10.0.30.1/24"}]}`

// e2eFirewallLeases : le bail dnsmasq dont la carte réseau est celle de la
// machine distante des données fictives (e2eRemoteMAC) ; les baux Kea et ISC du
// faux restent ceux d'appareils inconnus.
const e2eFirewallLeases = `{"total":1,"rowCount":1,"current":1,"rows":[
	{"if_descr":"LAN","address":"192.168.1.20","hwaddr":"` + e2eRemoteMAC + `","hostname":"vps-lyon","expire":1800003600,"lease_type":"dynamic"}]}`

// e2eRemoteMAC : la carte réseau que la machine distante rapporte.
const e2eRemoteMAC = "aa:bb:cc:dd:ee:ff"

// e2eFirewallRules : de quoi remplir la grille « qui parle à qui » : des règles
// passantes, des refus en toutes lettres, une règle éteinte, une flottante.
func e2eFirewallRules() string {
	type rule struct{ iface, action, proto, source, destination, port, description string }
	rules := []rule{
		{"lan", "pass", "TCP", "192.168.1.0/24", "any", "443", "Web sortant"},
		{"lan", "pass", "UDP", "any", "(self)", "53", "DNS"},
		{"lan", "pass", "TCP", "any", "(self)", "22", "Administration SSH"},
		{"lan", "pass", "TCP", "192.168.1.20", "10.0.20.0/24", "8006", "Proxmox"},
		{"lan", "block", "any", "any", "wan", "", "Pas de VPS"},
		{"wan", "block", "any", "any", "(self)", "", "Pas d'accès à l'administration"},
		{"wan", "pass", "TCP", "any", "10.0.20.5", "443", "Proxy public"},
		{"opt1", "pass", "TCP", "10.0.20.0/24", "any", "80", "Mises à jour"},
		{"opt1", "block", "any", "any", "192.168.1.0/24", "", "La DMZ ne rentre pas"},
		{"opt1", "pass", "TCP", "any", "any", "25", "Courrier (éteinte)"},
		{"", "pass", "any", "any", "any", "", "Règle flottante"},
	}
	var rows []string
	for index, r := range rules {
		enabled := "1"
		if strings.Contains(r.description, "éteinte") {
			enabled = "0"
		}
		rows = append(rows, fmt.Sprintf(`{"uuid":"22222222-aaaa-bbbb-cccc-%012d","enabled":%q,"interface":%q,"action":%q,"direction":"in","protocol":%q,"source_net":%q,"destination_net":%q,"destination_port":%q,"description":%q}`,
			index, enabled, r.iface, r.action, r.proto, r.source, r.destination, r.port, r.description))
	}
	return fmt.Sprintf(`{"total":%d,"rowCount":%d,"current":1,"rows":[%s]}`, len(rows), len(rows), strings.Join(rows, ","))
}

// seedE2EApply passe un fichier modifié à l'annuaire : le journal des fichiers
// appliqués a une ligne, donc une page de détail.
func seedE2EApply(t *testing.T, ts *testServer) {
	t.Helper()
	exported := ts.exportedFile(t)
	file := strings.Replace(exported, "title: Nextcloud", "title: Nextcloud édité", 1)
	if file == exported {
		t.Fatal("le fichier exporté ne porte pas le titre à modifier")
	}
	var plan planJSON
	decodeAPI(t, callAPI(ts.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, "")), &plan)
	if got := callAPI(ts.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, plan.PlanHash)); got.Code != http.StatusCreated {
		t.Fatalf("apply: %d %s", got.Code, got.Body.String())
	}
}

// seedE2EEdge montre le projet Compose « edge » : traefik est déclaré sur deux
// machines (une ligne, deux instances), authelia et crowdsec tournent sur la
// machine distante sans être déclarés (deux propositions). C'est la forme de la
// base de Lucas : edge = traefik + authelia + crowdsec.
func seedE2EEdge(t *testing.T, ts *testServer) {
	t.Helper()
	for _, machineID := range []string{remoteID, machine.LocalID} {
		_, err := ts.directory.CreateService(context.Background(), directory.ServiceDefinition{
			MachineID: machineID, Name: "traefik", Nature: directory.NatureInfra, Origin: directory.OriginCompose, ComposeProject: "edge",
			Units: []directory.UnitDefinition{{Name: "traefik", Image: "traefik", Version: "v3.1"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	edge := func(id, service, image string) container.Observed {
		return container.Observed{
			ContainerID: strings.Repeat(id, 64), Name: "edge-" + service + "-1", Group: "edge", ComposeService: service, Image: image,
			State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour),
		}
	}
	ts.recordOnRemote(t,
		edge("6", "traefik", "traefik:v3.1"),
		edge("7", "authelia", "authelia/authelia:4.38"),
		edge("8", "crowdsec", "crowdsecurity/crowdsec:v1.6"),
	)
}

// seedE2ESystemd montre les unités systemd : la machine distante porte Forgejo
// (déclaré, sans port : l'invitation à le déclarer), une sauvegarde de nuit en
// échec et des unités que personne ne déclare ; la machine openCloud n'a pas
// l'unité de Caddy, que l'annuaire déclare (absente de la machine).
func seedE2ESystemd(t *testing.T, ts *testServer) {
	t.Helper()
	ctx := context.Background()
	unit := func(name string, active systemdunits.Active, sub systemdunits.Sub) systemdunits.Unit {
		return systemdunits.Unit{Name: name, Load: systemdunits.LoadLoaded, Active: active, Sub: sub}
	}
	remote := systemdunits.Report{Available: true, Units: []systemdunits.Unit{
		unit("cron.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		unit("forgejo.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		unit("nightly-backup.service", systemdunits.ActiveFailed, systemdunits.SubFailed),
		unit("opencloud-agent.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		unit("ssh.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		unit("unattended-upgrades.service", systemdunits.ActiveInactive, systemdunits.SubDead),
	}}
	local := systemdunits.Report{Available: true, Units: []systemdunits.Unit{
		unit("cron.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		unit("ssh.service", systemdunits.ActiveActive, systemdunits.SubRunning),
	}}
	for machineID, report := range map[string]systemdunits.Report{remoteID: remote, machine.LocalID: local} {
		if err := ts.systemd.Record(ctx, machineID, report); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: remoteID, Name: "forgejo", Title: "Forgejo", Nature: directory.NatureApplication, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "forgejo", SystemdUnit: "forgejo.service", Version: "9.0.1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ts.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Provides: []directory.Capability{directory.CapabilityProxy},
		Units:    []directory.UnitDefinition{{Name: "caddy", SystemdUnit: "caddy.service", Version: "2.8.4", Ports: []string{"80/tcp", "443"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// seedE2EProxmox déclare un hôte Proxmox garni sur la machine distante, lu avec
// un jeton PVEAuditor : les archives ne sont pas lisibles, la fiche dit le droit
// qui manque, les VM disent « Inconnue ». Son jeton « !complet » (même secret
// fictif) lit aussi les archives : le parcours s'en sert pour déclarer un second
// hôte sur la machine openCloud. La VM 5012 porte le nom d'hôte de la machine
// openCloud : le rapprochement la lie.
func seedE2EProxmox(t *testing.T, ts *testServer) {
	t.Helper()
	wireProxmox(t, ts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	api := newFakeProxmox(t)
	api.rich, api.auditor = true, true
	api.setGuestName("opencloud-host")
	if got := putProxmox(ts, remoteID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("proxmox: %d %s", got.Code, got.Body.String())
	}
}

// seedE2ELinks déclare la base de nextcloud comme son propre service (le
// depends_on des conteneurs de la planche devient le lien d'ordre de
// l'application vers elle), un lien écrit par l'opérateur entre deux machines,
// et un lien d'un outil que plus aucun rapport ne confirme depuis dix jours.
func seedE2ELinks(t *testing.T, ts *testServer) {
	t.Helper()
	ctx := context.Background()
	if _, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: remoteID, Name: "cloud-db", Nature: directory.NatureApplication, Origin: directory.OriginCompose, ComposeProject: "nextcloud",
		Units: []directory.UnitDefinition{{Name: "nextcloud-db", Image: "postgres", Version: "16.4"}},
	}); err != nil {
		t.Fatal(err)
	}
	cloud := e2eServiceNamed(t, ts, remoteID, "nextcloud")
	proxy := e2eServiceNamed(t, ts, machine.LocalID, "caddy")
	if _, err := ts.directory.DeclareLink(ctx, directory.LinkDefinition{FromServiceID: proxy.ID, ToServiceID: cloud.ID, ToUnitID: cloud.Units[0].ID, Port: 8080}); err != nil {
		t.Fatal(err)
	}
	// Deux applications avec une adresse publique : caddy, qui fournit la
	// capacité proxy, les joint par deux routes déduites (le parcours confirme
	// l'une, écarte l'autre).
	for _, name := range []string{"blog", "wiki"} {
		if _, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
			MachineID: remoteID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginContainer,
			PublicURL: "https://" + name + ".example.test",
			Units:     []directory.UnitDefinition{{Name: name, ContainerName: name, Image: "ghost"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	old := link.New(ts.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	old.SetClock(func() time.Time { return testNow.Add(-10 * 24 * time.Hour) })
	orders := []link.Seen{{From: link.ComposeKey("nextcloud", "nextcloud"), To: link.ContainerKey("legacy-cache"), Signal: link.SignalContainerLink}}
	if err := old.RecordSeen(ctx, remoteID, orders); err != nil {
		t.Fatal(err)
	}
}

func e2eServiceNamed(t *testing.T, ts *testServer, machineID, name string) directory.Service {
	t.Helper()
	found, err := ts.db.ListDeclaredServices(context.Background(), directory.Filter{MachineID: machineID})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range found {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("service %q absent de la machine %s", name, machineID)
	return directory.Service{}
}
