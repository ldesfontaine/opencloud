package opnsensetest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const (
	Key    = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789testkey" // gitleaks:allow -- fausse clé de test
	Secret = "ZyXwVuTsRqPoNmLkJiHgFeDcBa9876543210secret"  // #nosec G101 -- faux secret de test, gitleaks:allow
)

// Les chemins que le faux sert : ceux de l'API d'OPNsense.
const (
	VersionPath       = "/api/diagnostics/system/system_information"
	InterfacesPath    = "/api/interfaces/overview/interfacesInfo"
	RulesPath         = "/api/firewall/filter/searchRule"
	GatewaysPath      = "/api/routes/gateway/status"
	DnsmasqLeasesPath = "/api/dnsmasq/leases/search"
	KeaLeasesPath     = "/api/kea/leases4/search"
	ISCLeasesPath     = "/api/dhcpv4/leases/searchLease"
)

// Server est le faux OPNsense. Il garde la méthode et le chemin de chaque
// requête, et refuse toute autre paire de clés que Key et Secret.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
	answers  map[string]answer
}

type answer struct {
	status int
	body   string
}

func New(t *testing.T) *Server {
	t.Helper()
	fake := &Server{answers: map[string]answer{}}
	fake.Server = httptest.NewTLSServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (fake *Server) serve(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	fake.requests = append(fake.requests, r.Method+" "+r.URL.Path)
	override, overridden := fake.answers[r.URL.Path]
	fake.mu.Unlock()

	user, password, hasBasic := r.BasicAuth()
	if !hasBasic || user != Key || password != Secret {
		http.Error(w, `{"status":401,"message":"Authentication Failed"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if overridden {
		w.WriteHeader(override.status)
		_, _ = w.Write([]byte(override.body))
		return
	}
	body, found := RealisticAnswers[r.URL.Path]
	if !found {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write([]byte(body))
}

// Answer remplace la réponse d'un chemin : son statut et son corps.
func (fake *Server) Answer(path string, status int, body string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.answers[path] = answer{status: status, body: body}
}

// Requests rend « MÉTHODE chemin » de chaque requête reçue, dans l'ordre.
func (fake *Server) Requests() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.requests...)
}

// Pin est l'empreinte SHA-256 de son certificat, telle qu'on la déclare.
func (fake *Server) Pin() string {
	sum := sha256.Sum256(fake.Certificate().Raw)
	return hex.EncodeToString(sum[:])
}

// RealisticAnswers sont les réponses des points d'accès lus, de la forme que
// les contrôleurs d'OPNsense rendent (grilles « rows », « items » des
// passerelles, « versions » du tableau de bord).
var RealisticAnswers = map[string]string{
	VersionPath: `{"name":"fw.example.lan","versions":["OPNsense 25.1.6-amd64","FreeBSD 14.2-RELEASE-p3","OpenSSL 3.0.16 11 Feb 2025"],"updates":"Click to check for updates."}`,
	InterfacesPath: `{"total":3,"rowCount":3,"current":1,"rows":[
		{"identifier":"wan","device":"igb0","description":"WAN","status":"up","enabled":true,"link_type":"dhcp","addr4":"203.0.113.2/24","addr6":"","gateways":["WAN_DHCP"]},
		{"identifier":"lan","device":"igb1","description":"LAN","status":"up","enabled":true,"link_type":"static","addr4":"192.168.1.1/24","addr6":"fd00::1/64"},
		{"identifier":"","device":"igb2","description":"Unassigned Interface","status":"down","enabled":false,"addr4":""}]}`,
	RulesPath: `{"total":3,"rowCount":3,"current":1,"rows":[
		{"uuid":"11111111-aaaa-bbbb-cccc-000000000001","enabled":"1","sequence":"1","action":"pass","direction":"in","interface":"lan","ipprotocol":"inet","protocol":"TCP","source_net":"192.168.1.0/24","source_port":"","destination_net":"any","destination_port":"443","description":"Web sortant"},
		{"uuid":"11111111-aaaa-bbbb-cccc-000000000002","enabled":"0","sequence":"2","action":"block","direction":"in","interface":"wan","ipprotocol":"inet","protocol":"any","source_net":"any","source_port":"","destination_net":"any","destination_port":"","description":""},
		{"uuid":"11111111-aaaa-bbbb-cccc-000000000003","sequence":"3","action":"pass","direction":"in","interface":"lan,opt1","protocol":"UDP","source_net":"any","source_port":"","destination_net":"(self)","destination_port":"53","description":"DNS"}]}`,
	DnsmasqLeasesPath: `{"total":1,"rowCount":1,"current":1,"rows":[
		{"if":"igb1","if_name":"lan","if_descr":"LAN","address":"192.168.1.20","hwaddr":"aa:bb:cc:dd:ee:ff","hostname":"nas","expire":1800003600,"lease_type":"dynamic","is_reserved":[]}],"interfaces":{"lan":"LAN"}}`,
	KeaLeasesPath: `{"total":1,"rowCount":1,"current":1,"rows":[
		{"if":"igb1","if_name":"lan","if_descr":"LAN","address":"192.168.1.30","hwaddr":"11:22:33:44:55:66","hostname":"imprimante","expire":0,"state":0,"lease_type":"static"}]}`,
	ISCLeasesPath: `{"total":1,"rowCount":1,"current":1,"rows":[
		{"address":"192.168.1.40","type":"dynamic","mac":"de:ad:be:ef:00:01","starts":"2026/10/28 10:00:00","ends":"2026/10/28 12:00:00","hostname":"vieux","descr":"","if_descr":"LAN","if":"lan","state":"active","status":"online","man":""}]}`,
	GatewaysPath: `{"status":"ok","items":[
		{"name":"WAN_DHCP","address":"203.0.113.1","status":"none","status_translated":"Online","loss":"0.0 %","delay":"1.4 ms","stddev":"0.2 ms","monitor":"203.0.113.1"},
		{"name":"BACKUP","address":"198.51.100.1","status":"down","status_translated":"Offline","loss":"100.0 %","delay":"~","stddev":"~","monitor":"198.51.100.1"},
		{"name":"SANS_SONDE","address":"dynamic","status":"none","status_translated":"Online","loss":"~","delay":"~","stddev":"~","monitor":"~"}]}`,
}
