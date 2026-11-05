package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
	"github.com/ldesfontaine/opencloud/internal/hostconn"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sampler"
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
	seedE2ERestic(t, ts)
	seedE2EAnchors(t, ts)
	seedE2EZFSCache(t, ts)
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
	seedE2EFirewallWithoutExport(t, ts)
}

// seedE2EFirewallWithoutExport déclare « Secours » : sa clé répond aux
// relevés, mais l'export de la configuration lui est refusé (il manque le
// privilège « Diagnostics: Configuration History »). Le parcours de B3 y vérifie
// que l'assistant dit le libellé exact.
func seedE2EFirewallWithoutExport(t *testing.T, ts *testServer) {
	t.Helper()
	fake := opnsensetest.New(t)
	fake.Answer(opnsensetest.ExportPath, http.StatusForbidden, `{"status":403,"message":"Forbidden"}`)
	request := firewallRequestFor(fake, opnsensetest.Secret)
	request.Name = "Secours"
	if got := callAPI(ts.Server, http.MethodPost, "/api/firewalls", request); got.Code != http.StatusCreated {
		t.Fatalf("declare Secours: %d %s", got.Code, got.Body.String())
	}
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
	seedE2EEnvironment(t, ts)
	seedE2EHostConnections(t, ts)
}

// seedE2EHostConnections déclare « dashboard » (un conteneur qui publie le port
// 3001 de la machine distante) et fait rapporter à l'agent qui l'a joint : un
// compte de la machine, un portable du réseau local et Internet. Le port 9100
// n'est publié par personne : la connexion qui y arrive ne donne aucun lien.
func seedE2EHostConnections(t *testing.T, ts *testServer) {
	t.Helper()
	ctx := context.Background()
	if _, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: remoteID, Name: "dashboard", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units: []directory.UnitDefinition{{Name: "dashboard", ContainerName: "dashboard", Image: "grafana", Ports: []string{"3001:3000"}}},
	}); err != nil {
		t.Fatal(err)
	}
	report := hostconn.Report{Available: true, Connections: []hostconn.Connection{
		{Port: 3001, Origin: hostconn.OriginAccount, Name: "backup"},
		{Port: 3001, Origin: hostconn.OriginAddress, Name: "192.168.1.20"},
		{Port: 3001, Origin: hostconn.OriginInternet},
		{Port: 9100, Origin: hostconn.OriginInternet},
	}}
	// Vu il y a trois heures, puis redit à l'instant par le même rapport : la
	// première vue date, la dernière est fraîche.
	ts.hostConns.SetClock(func() time.Time { return testNow.Add(-3 * time.Hour) })
	if err := ts.hostConns.Record(ctx, remoteID, report); err != nil {
		t.Fatal(err)
	}
	ts.hostConns.SetClock(func() time.Time { return testNow.Add(-5 * time.Minute) })
	if err := ts.hostConns.Record(ctx, remoteID, report); err != nil {
		t.Fatal(err)
	}
	ts.hostConns.SetClock(func() time.Time { return testNow })
}

// seedE2EEnvironment déclare « notes » et sa base « notes-db » (deux services
// de conteneurs de la machine distante) et pose le lien que l'environnement de
// « notes » désigne, vu il y a deux heures : le lien que l'agent écrit avec le
// droit observe.env. L'agent de la machine distante ne l'a pas (voir
// seedE2EBackups) : la fiche le dit et propose de ré-enrôler.
func seedE2EEnvironment(t *testing.T, ts *testServer) {
	t.Helper()
	ctx := context.Background()
	for _, name := range []string{"notes", "notes-db"} {
		if _, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
			MachineID: remoteID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginContainer,
			Units: []directory.UnitDefinition{{Name: name, ContainerName: name, Image: "notes"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	seen := link.New(ts.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	seen.SetClock(func() time.Time { return testNow.Add(-2 * time.Hour) })
	read := []link.Seen{{From: link.ContainerKey("notes"), To: link.ContainerKey("notes-db"), Signal: link.SignalEnvironment, Port: 5432}}
	if err := seen.RecordSeen(ctx, remoteID, read); err != nil {
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

// Les machines restic des données fictives : pi, la cible en marche ; docker1
// et pve-nord, deux sources (l'une en direct, l'autre par le relais) ; pve, un
// hôte Proxmox qui peut sauvegarder ses VM ; nas, qui a le droit d'être cible
// sans l'être encore. Des noms d'exemple, des adresses de documentation.
const (
	e2ePiID     = "33333333-4444-4555-8666-777777777777"
	e2eDockerID = "44444444-5555-4666-8777-888888888888"
	e2ePveID    = "55555555-6666-4777-8888-999999999999"
	e2eNorthID  = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	e2eNasID    = "77777777-8888-4999-8aaa-cccccccccccc"
	e2ePiHost   = "192.0.2.10"
	e2ePiPort   = 8000
)

// Les ancres de confiance locales (internal/anchor) : pve-ancres, un hôte dont
// l'administrateur n'a posé aucune ancre (ses ordres de sauvegarde sont refusés,
// l'écran donne la commande) ; pve-illisible, dont le fichier porte des valeurs
// qui ne se lisent pas. pve et les autres machines n'ont rien dit de leurs
// ancres : des agents d'avant, que le serveur ne juge pas.
const (
	e2eAnchorsNoneID   = "aaaaaaaa-1111-4222-8333-444444444444"
	e2eAnchorsBrokenID = "bbbbbbbb-1111-4222-8333-555555555555"
)

func seedE2EAnchors(t *testing.T, ts *testServer) {
	t.Helper()
	rights := "observe,restic.produce,backup.produce=proxmox-vzdump"
	// Sa version lit le préfixe de backup.prune : il peut aussi être destination.
	enrolled, _ := ts.enroll(t, "pve-ancres", e2eAnchorsNoneID)
	none, err := ts.machines.Connect(context.Background(), enrolled, "192.0.2.30", "v0.6.0", rights+",backup.receive=/srv/sauvegardes", "", "")
	if err != nil {
		t.Fatal(err)
	}
	broken := e2eConnect(t, ts, "pve-illisible", e2eAnchorsBrokenID, rights)
	for id, reported := range map[string]string{e2eAnchorsNoneID: "none", e2eAnchorsBrokenID: "backup.recipient=!,backup.guest=!,restic.target-cert=!"} {
		found, err := ts.machines.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := ts.machines.NoteAnchors(context.Background(), found.Agent.ID, reported); err != nil {
			t.Fatal(err)
		}
	}
	// pve-ancres peut sauvegarder ses VM avec restic : l'assistant l'offre, et
	// son envoi se heurte aux ancres.
	api := newFakeProxmox(t)
	api.setGuestName("compta")
	request := api.request(pveTokenSecret)
	request.TokenID = pveFullTokenID
	if got := putProxmox(ts, e2eAnchorsNoneID, request); got.Code != http.StatusOK {
		t.Fatalf("proxmox pve-ancres: %d %s", got.Code, got.Body.String())
	}
	for _, id := range []string{e2eAnchorsNoneID, e2eAnchorsBrokenID} {
		if _, err := ts.db.ReplaceHostReport(context.Background(), id, machine.KindPhysical, hostinfo.Report{}); err != nil {
			t.Fatal(err)
		}
	}
	go e2eIgnoreOrders(none)
	go e2eIgnoreOrders(broken)

	// Un plan de sauvegarde de la VM 5012 de pve-ancres : sa dernière passe est
	// refusée par les ancres (le serveur n'envoie pas l'ordre), la page
	// Sauvegardes dit pourquoi et donne la commande.
	created := callAPI(ts.Server, http.MethodPost, "/api/backups/destinations", map[string]any{"name": "sauvegardes-ancres", "machine_id": e2eAnchorsNoneID, "path": "/srv/sauvegardes"})
	if created.Code != http.StatusCreated {
		t.Fatalf("destination pve-ancres: %d %s", created.Code, created.Body.String())
	}
	var destination backupDestinationJSON
	decodeAPI(t, created, &destination)
	plan := map[string]any{
		"name": "compta", "source_kind": "agent", "source_machine_id": e2eAnchorsNoneID, "producer": "proxmox-vzdump",
		"selector": "5012", "destination_id": destination.ID, "keep": 7,
	}
	if got := callAPI(ts.Server, http.MethodPut, "/api/backups/plans/compta", plan); got.Code != http.StatusOK {
		t.Fatalf("plan compta: %d %s", got.Code, got.Body.String())
	}
	// La passe de la nuit : la première cible est refusée par les ancres (le
	// serveur n'envoie pas l'ordre) et la ligne de l'historique garde la raison.
	ts.backups.RunIfDue(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(callAPI(ts.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(), `"fix_command"`) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("la passe de compta n'a pas été refusée par les ancres")
}

// seedE2ERestic remplit la page Sauvegardes : une cible restic en marche, une
// sauvegarde de service passée en direct, une autre par le relais (pve-nord ne
// joint pas pi:8000), des VM que Proxmox sauvegarde de lui-même (la tâche
// nuit-pi), et des machines à qui il manque un droit. Les flux des machines
// restent ouverts : elles sont en ligne, et chaque ordre qu'un écran envoie
// part sans réponse.
func seedE2ERestic(t *testing.T, ts *testServer) {
	t.Helper()
	pi := e2eConnect(t, ts, "pi", e2ePiID, "observe,restic.target=/srv/restic,restic.verify,restic.browse")
	docker := e2eConnect(t, ts, "docker1", e2eDockerID, "observe,restic.produce,restic.docker,restic.restore,files.write=/srv")
	north := e2eConnect(t, ts, "pve-nord", e2eNorthID, "observe,restic.produce")
	pve := e2eConnect(t, ts, "pve", e2ePveID, "observe,restic.produce,backup.produce=proxmox-vzdump")
	nas := e2eConnect(t, ts, "nas", e2eNasID, "observe,restic.target=/srv/restic")

	e2eReadyTarget(t, ts, pi)
	paperless := e2eComposeService(t, ts, e2eDockerID, "paperless")
	e2eComposeService(t, ts, e2eDockerID, "photos")
	vaultwarden := e2eComposeService(t, ts, e2eNorthID, "vaultwarden")
	e2eResticBackup(t, ts, docker, pi, e2eDockerID, "paperless", paperless, agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: e2ePiHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("a", 64), BytesAdded: 42 << 20, DurationSeconds: 95,
	})
	// vaultwarden existe depuis vingt-trois jours, sauvegardé ce matin, et son
	// dépôt n'a jamais été testé (voir seedE2ESupervision).
	ts.restic.SetClock(func() time.Time { return testNow.Add(-23 * 24 * time.Hour) })
	vaultwardenBackup := e2eCreateResticBackup(t, ts, north, pi, e2eNorthID, "vaultwarden", vaultwarden)
	ts.restic.SetClock(func() time.Time { return testNow })
	e2eRunResticBackup(t, ts, north, e2eNorthID, vaultwardenBackup, agentwire.ResticRunReport{
		Path: agentwire.ResticPathRelay, Reason: agentwire.ResticTCPUnreachable, TargetHost: e2ePiHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("b", 64), BytesAdded: 200 << 10, DurationSeconds: 12,
	})

	seedE2EWholeServices(t, ts, docker, pi)
	seedE2ESupervision(t, ts, docker, pi)

	// Deux hôtes Proxmox de plus, leurs VM sauvegardées par la tâche nuit-pi :
	// pve peut aussi les sauvegarder avec restic, pve-nord n'a pas backup.produce.
	for id, guest := range map[string]string{e2ePveID: "web", e2eNorthID: "mail"} {
		api := newFakeProxmox(t)
		api.setGuestName(guest)
		request := api.request(pveTokenSecret)
		request.TokenID = pveFullTokenID
		if got := putProxmox(ts, id, request); got.Code != http.StatusOK {
			t.Fatalf("proxmox %s: %d %s", id, got.Code, got.Body.String())
		}
	}
	// Leur type rapporté : des machines physiques, docker1 une VM.
	for id, kind := range map[string]machine.Kind{e2ePiID: machine.KindPhysical, e2eDockerID: machine.KindVM, e2ePveID: machine.KindPhysical, e2eNorthID: machine.KindPhysical, e2eNasID: machine.KindPhysical} {
		if _, err := ts.db.ReplaceHostReport(context.Background(), id, kind, hostinfo.Report{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []*machine.Session{north, pve, nas} {
		go e2eIgnoreOrders(session)
	}
	go e2eServeBrowse(t, ts, pi)
	go e2eServeRestore(t, ts, docker)
	seedE2EMissingBinaries(t, ts)
}

// seedE2EWholeServices montre le service entier (B2) : recettes, sur docker1,
// a un volume et une base postgres, et sa base a échoué cette nuit après une
// première nuit réussie ; photos (docker1) a un volume que rien ne sauvegarde ;
// carnet (pve-nord) a une base, mais pve-nord n'a pas restic.docker.
func seedE2EWholeServices(t *testing.T, ts *testServer, docker, pi *machine.Session) {
	t.Helper()
	ctx := context.Background()
	service := func(machineID, name string, units ...directory.UnitDefinition) string {
		created, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
			MachineID: machineID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginCompose, ComposeProject: name, Units: units,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	observed := func(name, group, composeService, image, digit string) container.Observed {
		return container.Observed{
			ContainerID: strings.Repeat(digit, 64), Name: name, Group: group, Image: image, ImageID: "sha256:" + strings.Repeat(digit, 64),
			ComposeService: composeService, State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour),
		}
	}
	recettes := service(e2eDockerID, "recettes", directory.UnitDefinition{Name: "app", Image: "recettes", Version: "1.0"}, directory.UnitDefinition{Name: "db", Image: "postgres", Version: "16"})
	service(e2eNorthID, "carnet", directory.UnitDefinition{Name: "db", Image: "mariadb", Version: "11"})
	record := func(machineID string, report container.Report) {
		if err := ts.containers.Record(ctx, machineID, report); err != nil {
			t.Fatal(err)
		}
	}
	record(e2eDockerID, container.Report{
		Complete: true,
		Inventory: []container.Observed{
			observed("recettes-app-1", "recettes", "app", "recettes:1.0", "d"),
			observed("recettes-db-1", "recettes", "db", "postgres:16", "e"),
			observed("photos-1", "photos", "photos", "photos:1.0", "f"),
		},
		VolumesComplete: true,
		Volumes: []container.VolumeReport{
			{Name: "recettes_data", Driver: "local", UsedBy: []string{"recettes-app-1"}},
			{Name: "photos_data", Driver: "local", UsedBy: []string{"photos-1"}},
		},
	})
	record(e2eNorthID, container.Report{
		Complete:  true,
		Inventory: []container.Observed{observed("carnet-db-1", "carnet", "db", "mariadb:11", "9")},
	})
	good := agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: e2ePiHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("c", 64), BytesAdded: 3 << 20, DurationSeconds: 40,
		Parts: []agentwire.ResticPartReport{
			{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 1 << 20},
			{Kind: agentwire.ResticPartVolume, Name: "recettes_data", State: agentwire.ResticPartOK, BytesAdded: 1 << 20},
			{Kind: agentwire.ResticPartDatabase, Name: "recettes-db-1", State: agentwire.ResticPartOK, BytesAdded: 1 << 20},
		},
	}
	e2eResticBackup(t, ts, docker, pi, e2eDockerID, "recettes", recettes, good)
	// La nuit suivante : le dossier et le volume passent, la base échoue.
	bad := good
	bad.Parts = []agentwire.ResticPartReport{
		{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 10 << 10},
		{Kind: agentwire.ResticPartVolume, Name: "recettes_data", State: agentwire.ResticPartOK, BytesAdded: 10 << 10},
		{Kind: agentwire.ResticPartDatabase, Name: "recettes-db-1", State: agentwire.ResticPartFailed, Reason: agentwire.ReasonDumpFailed},
	}
	bad.BytesAdded = 20 << 10
	e2eRunAgain(t, ts, docker, e2eDockerID, "recettes", agentwire.ActionFailed, agentwire.ReasonDumpFailed, bad)
}

// e2eRunAgain relance la sauvegarde nommée et lui fait rapporter ce résultat.
func e2eRunAgain(t *testing.T, ts *testServer, source *machine.Session, sourceID, name string, state agentwire.ActionState, reason string, run agentwire.ResticRunReport) {
	t.Helper()
	var list struct {
		Sources []struct {
			Name        string `json:"name"`
			Protections []struct {
				Origin string `json:"origin"`
				ID     string `json:"id"`
			} `json:"protections"`
		} `json:"sources"`
	}
	decodeAPI(t, callAPI(ts.Server, http.MethodGet, "/api/backups/overview", nil), &list)
	id := ""
	for _, candidate := range list.Sources {
		for _, protection := range candidate.Protections {
			if candidate.Name == name && protection.Origin == "restic" {
				id = protection.ID
			}
		}
	}
	if id == "" {
		t.Fatalf("no restic backup for %s", name)
	}
	if got := callAPI(ts.Server, http.MethodPost, "/api/machines/"+sourceID+"/restic-backups/"+id+"/actions/run", map[string]any{}); got.Code != http.StatusCreated {
		t.Fatalf("run again: %d %s", got.Code, got.Body.String())
	}
	order := nextOrder(t, source)
	result := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: order.RunID, State: state, Reason: reason, Restic: &run}}}
	if got := signalAnnouncingSections(ts, source.Token, agentwire.SignalRequest{Actions: &result}); got.Code != http.StatusNoContent {
		t.Fatalf("run report: %d %s", got.Code, got.Body.String())
	}
	// Le dépôt reste réservé jusqu'à la fin du suivi, qui lit le résultat un
	// instant après : sans cette attente, l'étape suivante sur le même dépôt
	// (une vérification) pouvait recevoir restic.backup_running, au gré de la charge.
	ts.restic.WaitBackground()
}

// Deux machines à qui il manque un binaire : coffre (arm64) est une cible
// dont l'agent ne trouve pas rest-server ; atelier tient restic.produce sans
// restic, et déclare le service grimoire.
const (
	e2eCoffreID  = "88888888-9999-4aaa-8bbb-dddddddddddd"
	e2eAtelierID = "99999999-aaaa-4bbb-8ccc-eeeeeeeeeeee"
)

func seedE2EMissingBinaries(t *testing.T, ts *testServer) {
	t.Helper()
	coffre := e2eConnectAs(t, ts, "coffre", e2eCoffreID, "Debian 12", "arm64", "observe,restic.target=/srv/restic", "")
	got := callAPI(ts.Server, http.MethodPost, "/api/machines/"+e2eCoffreID+"/actions/restic-target-setup", map[string]any{"host": "192.0.2.40", "port": 8000})
	if got.Code != http.StatusNoContent {
		t.Fatalf("setup coffre: %d %s", got.Code, got.Body.String())
	}
	nextOrder(t, coffre)
	report := agentwire.ResticReport{Target: &agentwire.ResticTargetReport{State: agentwire.ResticTargetError, Reason: agentwire.ResticErrorBinaryMissing}}
	if got := signalAnnouncingSections(ts, coffre.Token, agentwire.SignalRequest{Restic: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("coffre report: %d %s", got.Code, got.Body.String())
	}
	atelier := e2eConnectAs(t, ts, "atelier", e2eAtelierID, "Ubuntu 24.04 LTS", "amd64", "observe,restic.produce", "restic.produce="+agentwire.ReasonResticMissing)
	e2eComposeService(t, ts, e2eAtelierID, "grimoire")
	for _, id := range []string{e2eCoffreID, e2eAtelierID} {
		if _, err := ts.db.ReplaceHostReport(context.Background(), id, machine.KindPhysical, hostinfo.Report{}); err != nil {
			t.Fatal(err)
		}
	}
	go e2eIgnoreOrders(coffre)
	go e2eIgnoreOrders(atelier)
}

// e2eConnectAs enrôle une machine sous ce système et cette architecture, puis
// ouvre son flux avec ces droits rapportés et ceux qu'elle ne peut pas exercer.
func e2eConnectAs(t *testing.T, ts *testServer, name, id, system, arch, rights, unavailable string) *machine.Session {
	t.Helper()
	ctx := context.Background()
	token, _, err := ts.machines.CreateToken(ctx, name, machine.RoleGeneric, nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := ts.machines.Enroll(ctx, machine.Enrollment{
		MachineID: id, PublicKey: public, Token: token, Hostname: name, Address: "192.0.2.31", OS: system, Arch: arch, AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := ts.machines.Connect(ctx, enrolled, "192.0.2.31", "v0.0.1", rights, "", unavailable)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// e2eConnect enrôle une machine et ouvre son flux avec ces droits rapportés.
func e2eConnect(t *testing.T, ts *testServer, name, id, rights string) *machine.Session {
	t.Helper()
	enrolled, _ := ts.enroll(t, name, id)
	session, err := ts.machines.Connect(context.Background(), enrolled, "192.0.2.30", "v0.0.1", rights, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// e2eReadyTarget fait de pi une cible, puis lui fait rapporter rest-server en
// marche : 2 Tio, dont 600 Gio libres.
func e2eReadyTarget(t *testing.T, ts *testServer, pi *machine.Session) {
	t.Helper()
	e2eReadyTargetAt(t, ts, pi, e2ePiID, e2ePiHost, e2ePiPort)
}

// e2eReadyTargetAt fait de même pour une autre machine, à son adresse.
func e2eReadyTargetAt(t *testing.T, ts *testServer, target *machine.Session, id, host string, port int) {
	t.Helper()
	got := callAPI(ts.Server, http.MethodPost, "/api/machines/"+id+"/actions/restic-target-setup", map[string]any{"host": host, "port": port})
	if got.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", got.Code, got.Body.String())
	}
	nextOrder(t, target)
	certificate, fingerprint := testCertificate(t)
	report := agentwire.ResticReport{Target: &agentwire.ResticTargetReport{
		State: agentwire.ResticTargetRunning, Version: "rest-server 0.14.0", Host: host, Port: port,
		Certificate: certificate, Fingerprint: fingerprint, RepoBytes: 1 << 40, DiskTotal: 2 << 40, DiskFree: 600 << 30,
		RetentionFloor: &agentwire.ResticRetentionFloor{KeepDaily: 7},
	}}
	if got := signalAnnouncingSections(ts, target.Token, agentwire.SignalRequest{Restic: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("target report: %d %s", got.Code, got.Body.String())
	}
}

func e2eComposeService(t *testing.T, ts *testServer, machineID, name string) string {
	t.Helper()
	service, err := ts.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machineID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginCompose, ComposeProject: name,
		Units: []directory.UnitDefinition{{Name: name, Image: name, Version: "1.0"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service.ID
}

// e2eResticBackup déclare la sauvegarde d'un service vers pi, lie la source
// à la cible, la lance et fait rapporter son exécution.
func e2eResticBackup(t *testing.T, ts *testServer, source, pi *machine.Session, sourceID, name, serviceID string, run agentwire.ResticRunReport) {
	t.Helper()
	created := e2eCreateResticBackup(t, ts, source, pi, sourceID, name, serviceID)
	e2eRunResticBackup(t, ts, source, sourceID, created, run)
}

// e2eCreateResticBackup déclare la sauvegarde d'un service vers pi et lit les
// deux ordres du lien : restic.provision à la source, restic.user.add à la
// cible. Elle rend son identifiant.
func e2eCreateResticBackup(t *testing.T, ts *testServer, source, pi *machine.Session, sourceID, name, serviceID string) string {
	t.Helper()
	return e2eCreateResticBackupTo(t, ts, source, pi, sourceID, e2ePiID, name, serviceID)
}

// e2eCreateResticBackupTo fait de même vers la cible donnée.
func e2eCreateResticBackupTo(t *testing.T, ts *testServer, source, target *machine.Session, sourceID, targetID, name, serviceID string) string {
	t.Helper()
	got := callAPI(ts.Server, http.MethodPost, "/api/machines/"+sourceID+"/restic-backups", map[string]any{
		"name": name, "scope": "service", "service_id": serviceID, "target_machine_id": targetID,
		"retention": map[string]int{"keep_daily": 14, "keep_weekly": 8, "keep_monthly": 3},
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	var created resticBackupCreatedJSON
	decodeAPI(t, got, &created)
	nextOrder(t, source)
	nextOrder(t, target)
	return created.ID
}

// e2eRunResticBackup lance la sauvegarde et fait rapporter son exécution.
func e2eRunResticBackup(t *testing.T, ts *testServer, source *machine.Session, sourceID, id string, run agentwire.ResticRunReport) {
	t.Helper()
	started := callAPI(ts.Server, http.MethodPost, "/api/machines/"+sourceID+"/restic-backups/"+id+"/actions/run", map[string]any{})
	if started.Code != http.StatusCreated {
		t.Fatalf("run: %d %s", started.Code, started.Body.String())
	}
	order := nextOrder(t, source)
	result := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &run}}}
	if got := signalAnnouncingSections(ts, source.Token, agentwire.SignalRequest{Actions: &result}); got.Code != http.StatusNoContent {
		t.Fatalf("run report: %d %s", got.Code, got.Body.String())
	}
	// Le dépôt reste réservé jusqu'à la fin du suivi, qui lit le résultat un
	// instant après : sans cette attente, l'étape suivante sur le même dépôt
	// (une vérification) pouvait recevoir restic.backup_running, au gré de la charge.
	ts.restic.WaitBackground()
}

// e2eIgnoreOrders lit les ordres d'une machine sans y répondre : un agent
// qui ne finit rien, sans que sa file se remplisse.
func e2eIgnoreOrders(session *machine.Session) {
	for range session.Events() {
	}
}

// seedE2ESupervision montre la supervision (B4) : le dépôt docker1 → pi a été
// vérifié par pi (deux instantanés relus) ; celui de pve-nord → pi, créé il y a
// vingt-trois jours (vaultwarden), n'a jamais été testé ; agenda (docker1) n'a
// pas réussi depuis quatre jours ; calendrier (docker1) est gardé par
// stockage, qui n'a pas le droit restic.verify. Les services sans plan (photos,
// carnet, la base d'openCloud) sont des sources sans plan. Le passage de la
// supervision ouvre les alertes. Le temps du composant restic est avancé à la
// main, puis remis.
func seedE2ESupervision(t *testing.T, ts *testServer, docker, pi *machine.Session) {
	t.Helper()
	at := func(moment time.Time) { ts.restic.SetClock(func() time.Time { return moment }) }
	defer at(testNow)
	agenda := e2eComposeService(t, ts, e2eDockerID, "agenda")
	at(testNow.Add(-4 * 24 * time.Hour))
	e2eResticBackup(t, ts, docker, pi, e2eDockerID, "agenda", agenda, agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: e2ePiHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("d", 64), BytesAdded: 5 << 20, DurationSeconds: 30,
	})
	at(testNow)

	paperless := e2eBackupID(t, ts, "paperless")
	got := callAPI(ts.Server, http.MethodPost, "/api/machines/"+e2eDockerID+"/restic-backups/"+paperless+"/actions/verify", map[string]any{})
	if got.Code != http.StatusCreated {
		t.Fatalf("verify: %d %s", got.Code, got.Body.String())
	}
	order := nextOrder(t, pi)
	verified := agentwire.ActionReport{Results: []agentwire.ActionResult{{
		RunID: order.RunID, State: agentwire.ActionSucceeded, Verify: &agentwire.ResticVerifyReport{Snapshots: 2, Files: 4, Bytes: 6 << 20},
	}}}
	if got := signalAnnouncingSections(ts, pi.Token, agentwire.SignalRequest{Actions: &verified}); got.Code != http.StatusNoContent {
		t.Fatalf("verify report: %d %s", got.Code, got.Body.String())
	}
	ts.restic.WaitBackground()
	seedE2EBlindTarget(t, ts, docker)
	seedE2EQuietRepository(t, ts, pi)
	ts.restic.Supervise(context.Background())
}

const e2eBureauID = "bbbbbbbb-cccc-4ddd-8eee-aaaaaaaaaaaa"

// seedE2EQuietRepository : intranet (bureau → pi), un dépôt que le parcours des
// autres écrans ne touche pas, pour que « Vérifier maintenant » y parte sans
// trouver le dépôt occupé par une sauvegarde lancée à la main plus tôt.
func seedE2EQuietRepository(t *testing.T, ts *testServer, pi *machine.Session) {
	t.Helper()
	bureau := e2eConnect(t, ts, "bureau", e2eBureauID, "observe,restic.produce")
	intranet := e2eComposeService(t, ts, e2eBureauID, "intranet")
	e2eResticBackup(t, ts, bureau, pi, e2eBureauID, "intranet", intranet, agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: e2ePiHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("f", 64), BytesAdded: 1 << 20, DurationSeconds: 8,
	})
	go e2eIgnoreOrders(bureau)
}

const (
	e2eStockageID   = "aaaaaaaa-bbbb-4ccc-8ddd-ffffffffffff"
	e2eStockageHost = "192.0.2.50"
)

// seedE2EBlindTarget : stockage, une seconde cible qui n'a pas le droit
// restic.verify. calendrier (docker1), sauvegardé chaque nuit chez elle depuis
// vingt-deux jours, ne peut donc pas être testé : la carte et l'alerte disent
// le droit qui manque.
func seedE2EBlindTarget(t *testing.T, ts *testServer, docker *machine.Session) {
	t.Helper()
	at := func(moment time.Time) { ts.restic.SetClock(func() time.Time { return moment }) }
	defer at(testNow)
	stockage := e2eConnect(t, ts, "stockage", e2eStockageID, "observe,restic.target=/srv/restic")
	e2eReadyTargetAt(t, ts, stockage, e2eStockageID, e2eStockageHost, e2ePiPort)
	calendrier := e2eComposeService(t, ts, e2eDockerID, "calendrier")
	at(testNow.Add(-22 * 24 * time.Hour))
	id := e2eCreateResticBackupTo(t, ts, docker, stockage, e2eDockerID, e2eStockageID, "calendrier", calendrier)
	at(testNow)
	e2eRunResticBackup(t, ts, docker, e2eDockerID, id, agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, TargetHost: e2eStockageHost, TargetPort: e2ePiPort,
		SnapshotID: strings.Repeat("e", 64), BytesAdded: 2 << 20, DurationSeconds: 20,
	})
	go e2eIgnoreOrders(stockage)
}

// e2eBackupID rend l'identifiant de la sauvegarde restic de ce nom.
func e2eBackupID(t *testing.T, ts *testServer, name string) string {
	t.Helper()
	got := callAPI(ts.Server, http.MethodGet, "/api/backups/overview", nil)
	var overview backupOverviewJSON
	decodeAPI(t, got, &overview)
	for _, source := range overview.Sources {
		for _, protection := range source.Protections {
			if protection.Origin == "restic" && protection.Name == name {
				return protection.ID
			}
		}
	}
	t.Fatalf("no restic backup named %s", name)
	return ""
}

// e2eServeBrowse fait répondre pi aux parcours de l'explorateur (B5) avec un
// même arbre fictif, quel que soit le dépôt : trois instantanés, des dossiers
// et quelques fichiers. Les autres ordres sont lus et laissés sans réponse.
func e2eServeBrowse(t *testing.T, ts *testServer, pi *machine.Session) {
	snapshots := []agentwire.BrowseSnapshot{
		{ID: strings.Repeat("c", 64), Time: "2026-09-12T02:30:00Z", Paths: []string{"/srv/appli"}},
		{ID: strings.Repeat("d", 64), Time: "2026-09-11T02:30:00Z", Paths: []string{"/srv/appli"}},
		{ID: strings.Repeat("e", 64), Time: "2026-09-05T02:30:00Z", Paths: []string{"/srv/appli"}},
	}
	tree := map[string][]agentwire.BrowseEntry{
		"/":                 {{Name: "srv", Kind: "dir", ModTime: "2026-09-12T02:30:00Z"}},
		"/srv":              {{Name: "appli", Kind: "dir", ModTime: "2026-09-12T02:30:00Z"}},
		"/srv/appli":        {{Name: "config", Kind: "dir", ModTime: "2026-07-01T10:00:00Z"}, {Name: "data", Kind: "dir", ModTime: "2026-09-12T02:30:00Z"}, {Name: "docker-compose.yml", Kind: "file", Size: 1229, ModTime: "2026-07-01T10:00:00Z"}, {Name: "app.ini", Kind: "file", Size: 3481, ModTime: "2026-07-01T10:05:00Z"}, {Name: "existe", Kind: "file", Size: 10, ModTime: "2026-07-01T10:05:00Z"}},
		"/srv/appli/config": {{Name: "app.ini", Kind: "file", Size: 3481, ModTime: "2026-07-01T10:05:00Z"}, {Name: "custom.css", Kind: "file", Size: 812, ModTime: "2026-05-02T08:00:00Z"}},
	}
	for event := range pi.Events() {
		if event.Name != agentwire.EventOrder {
			continue
		}
		signed, err := agentwire.DecodeSignedOrder(event.Data)
		if err != nil {
			continue
		}
		var body agentwire.OrderBody
		if json.Unmarshal(signed.Body, &body) != nil || body.Kind != agentwire.KindResticBrowse {
			continue
		}
		var params agentwire.ResticBrowseParams
		if json.Unmarshal(body.Params, &params) != nil {
			continue
		}
		report := agentwire.ResticBrowseReport{}
		if params.Snapshot == "" {
			report.Snapshots = snapshots
		} else {
			report.Entries = tree[params.Path]
		}
		e2eReportResult(t, ts, pi, agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionSucceeded, Browse: &report})
	}
}

// e2eReportResult rapporte le résultat d'un ordre. Le débit des signaux d'une
// machine est borné (une réponse toutes les deux secondes une fois le seau
// vidé par l'amorçage) : comme un vrai agent, la machine reprend après un refus
// de débit.
func e2eReportResult(t *testing.T, ts *testServer, session *machine.Session, result agentwire.ActionResult) {
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{result}}
	for attempt := 0; attempt < 40; attempt++ {
		got := signalAnnouncingSections(ts, session.Token, agentwire.SignalRequest{Actions: &report})
		if got.Code == http.StatusNoContent {
			return
		}
		if got.Code != http.StatusTooManyRequests {
			t.Errorf("report: %d %s", got.Code, got.Body.String())
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// e2eServeRestore fait répondre docker1 aux restaurations de l'explorateur (B5) :
// un chemin qui se termine par « existe » trouve déjà sa copie (refus
// path_exists), tout autre est restauré à côté de l'original.
func e2eServeRestore(t *testing.T, ts *testServer, source *machine.Session) {
	for event := range source.Events() {
		if event.Name != agentwire.EventOrder {
			continue
		}
		signed, err := agentwire.DecodeSignedOrder(event.Data)
		if err != nil {
			continue
		}
		var body agentwire.OrderBody
		if json.Unmarshal(signed.Body, &body) != nil || body.Kind != agentwire.KindResticRestore {
			continue
		}
		var params agentwire.ResticRestoreParams
		if json.Unmarshal(body.Params, &params) != nil {
			continue
		}
		result := agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionSucceeded,
			Output: []agentwire.OutputLine{{Stream: "stdout", Line: "restored " + agentwire.RestoreDestination(params.Path, params.Stamp)}}}
		if strings.HasSuffix(params.Path, "existe") {
			result = agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonPathExists}
		}
		e2eReportResult(t, ts, source, result)
	}
}

// seedE2EZFSCache : pve-ancres est un Pi de sauvegarde en ZFS. Sa mémoire
// utilisée est déjà corrigée (1,2 Go) ; l'ARC de 9,9 Go est montré à part.
// La machine openCloud (sans ZFS) et pve-illisible (aucune mesure) disent les
// deux autres états de la tuile.
func seedE2EZFSCache(t *testing.T, ts *testServer) {
	t.Helper()
	reading := testReading(testNow.Add(-10*time.Second), 4)
	reading.MemUsed, reading.MemTotal = 1_288_490_189, 16_106_127_360
	cache := int64(9_925_000_000)
	reading.ZFSCache = &cache
	if err := ts.resources.Record(context.Background(), e2eAnchorsNoneID, []sampler.Reading{reading}); err != nil {
		t.Fatal(err)
	}
}
