package agentwire

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

func publicCertificate(t *testing.T) (string, string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "nas"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"nas"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return certificatePEM, CertificateFingerprint(der), key
}

const sampleBcrypt = "$2y$10$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"

// Les noms sont lus par les agents installés : figés.
func TestResticKinds_HaveFrozenNames(t *testing.T) {
	names := map[Kind]string{
		KindResticTargetSetup: "restic.target.setup",
		KindResticUserAdd:     "restic.user.add",
		KindResticUserRemove:  "restic.user.remove",
		KindResticProvision:   "restic.provision",
		KindResticBackup:      "restic.backup",
		KindResticRetain:      "restic.retain",
	}
	for kind, name := range names {
		if string(kind) != name {
			t.Errorf("%s", kind)
		}
	}
}

func TestResticUser_IsASingleLowercaseSegment(t *testing.T) {
	user, err := ResticUserFor("6F1C2A3B-0D4E-4F5A-8B6C-7D8E9F0A1B2C")
	if err != nil || user != "6f1c2a3b-0d4e-4f5a-8b6c-7d8e9f0a1b2c" {
		t.Fatalf("user %q %v", user, err)
	}
	for _, name := range []string{"", "-a", "a.b", "a/b", "..", "a:b", "a b", strings.Repeat("a", 64)} {
		if IsResticUser(name) {
			t.Errorf("%q passed", name)
		}
	}
	if _, err := ResticUserFor("../x"); err == nil {
		t.Error("../x passed")
	}
}

func TestResticParams_ValidateEachOrder(t *testing.T) {
	certificatePEM, _, _ := publicCertificate(t)
	valid := []interface{ Validate() error }{
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 8000, Hosts: []string{"192.0.2.10", "nas"}},
		ResticTargetSetupParams{Listen: "0.0.0.0", Port: 8000, Hosts: []string{"nas"}, RetentionFloor: &ResticRetentionFloor{KeepDaily: 14, KeepWeekly: 4}},
		ResticUserAddParams{User: "vps"},
		ResticUserRemoveParams{User: "vps"},
		ResticRetainParams{User: "vps", KeepDaily: 7},
		ResticRetainParams{User: "vps", KeepLast: 3, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12, KeepYearly: 1000},
		ResticProvisionParams{Target: "nas", Host: "192.0.2.10", Port: 8000, User: "vps", Certificate: certificatePEM},
		ResticBackupParams{Target: "nas", Scope: ResticScopeVM, VMID: 105, GuestType: GuestQEMU},
		ResticBackupParams{Target: "nas", Scope: ResticScopeService, Service: "wiki", ForceRelay: true},
	}
	for _, params := range valid {
		if err := params.Validate(); err != nil {
			t.Errorf("%+v: %v", params, err)
		}
	}
	invalid := []interface{ Validate() error }{
		ResticTargetSetupParams{Listen: "nas", Port: 8000, Hosts: []string{"nas"}},
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 0, Hosts: []string{"nas"}},
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 8000},
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 8000, Hosts: []string{"nas:8000"}},
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 8000, Hosts: []string{"a", "b", "c", "d", "e"}},
		ResticTargetSetupParams{Listen: "192.0.2.10", Port: 8000, Hosts: []string{"nas"}, RetentionFloor: &ResticRetentionFloor{KeepDaily: -1}},
		ResticUserAddParams{User: "../vps"},
		ResticRetainParams{User: "../vps", KeepDaily: 7},
		ResticRetainParams{User: "vps"},
		ResticRetainParams{User: "vps", KeepDaily: 7, KeepWeekly: -1},
		ResticRetainParams{User: "vps", KeepYearly: 1001},
		ResticUserRemoveParams{User: "vps/"},
		ResticProvisionParams{Target: "nas", Host: "nas/x", Port: 8000, User: "vps", Certificate: certificatePEM},
		ResticProvisionParams{Target: "nas", Host: "nas", Port: 8000, User: "vps", Certificate: "not a pem"},
		ResticBackupParams{Target: "nas", Scope: ResticScopeVM, VMID: 99, GuestType: GuestQEMU},
		ResticBackupParams{Target: "nas", Scope: ResticScopeVM, VMID: 105, GuestType: GuestQEMU, Service: "wiki"},
		ResticBackupParams{Target: "nas", Scope: ResticScopeService, Service: "../etc"},
		ResticBackupParams{Target: "nas", Scope: ResticScopeService, Service: ".hidden"},
		ResticBackupParams{Target: "nas", Scope: "host"},
	}
	for _, params := range invalid {
		if params.Validate() == nil {
			t.Errorf("%+v passed", params)
		}
	}
}

func TestResticReport_ValidatesTheTargetCertificateAgainstItsFingerprint(t *testing.T) {
	certificatePEM, fingerprint, key := publicCertificate(t)
	target := ResticTargetReport{
		State: ResticTargetRunning, Version: "rest-server 0.14.0", Host: "192.0.2.10", Port: 8000,
		Certificate: certificatePEM, Fingerprint: fingerprint, RepoBytes: 1 << 30, DiskTotal: 1 << 40, DiskFree: 1 << 39,
		Users: []string{"vps"},
	}
	if err := (ResticReport{Target: &target}).Validate(); err != nil {
		t.Fatal(err)
	}

	wrongFingerprint := target
	wrongFingerprint.Fingerprint = strings.Repeat("0", 64)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	withPrivateKey := target
	withPrivateKey.Certificate += string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	unknownState := target
	unknownState.State = "starting"
	reasonWhileRunning := target
	reasonWhileRunning.Reason = ResticErrorPortInUse
	openReason := target
	openReason.State, openReason.Reason = ResticTargetError, "something odd"
	hostileUser := target
	hostileUser.Users = []string{"vps\nroot"}
	multiline := target
	multiline.Version = "0.14\nforged"
	for name, report := range map[string]ResticTargetReport{
		"wrong fingerprint": wrongFingerprint, "private key": withPrivateKey, "state": unknownState,
		"reason while running": reasonWhileRunning, "open reason": openReason, "user": hostileUser, "version": multiline,
	} {
		if (ResticReport{Target: &report}).Validate() == nil {
			t.Errorf("%s passed", name)
		}
	}
	failing := ResticTargetReport{State: ResticTargetError, Reason: ResticErrorBinaryMissing}
	if err := (ResticReport{Target: &failing}).Validate(); err != nil {
		t.Errorf("a failing target without certificate: %v", err)
	}
}

// Liste blanche : un champ que le type ne nomme pas ne survit pas au
// décodage, quoi que l'agent envoie.
func TestResticReport_DropsFieldsOutsideTheWhitelist(t *testing.T) {
	sent := `{"target":{"state":"stopped","password":"p4ss","hash":"` + sampleBcrypt + `"},` +
		`"source":{"version":"restic 0.19.1","repository_password":"r3po","targets":["nas"],"links":[{"target":"nas","hash":"` + sampleBcrypt + `","password":"p4ss"}]}}`
	var report ResticReport
	if err := json.Unmarshal([]byte(sent), &report); err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	kept, _ := json.Marshal(report)
	for _, secret := range []string{"p4ss", "r3po", "password", sampleBcrypt, "hash", "links"} {
		if strings.Contains(string(kept), secret) {
			t.Errorf("%s survived: %s", secret, kept)
		}
	}
	if report.Source == nil || len(report.Source.Targets) != 1 || report.Source.Targets[0] != "nas" {
		t.Errorf("source %+v", report.Source)
	}
	tooMany := ResticSourceReport{Targets: make([]string, 65)}
	for index := range tooMany.Targets {
		tooMany.Targets[index] = "nas"
	}
	if (ResticReport{Source: &tooMany}).Validate() == nil {
		t.Error("65 targets passed")
	}
	hostile := ResticSourceReport{Targets: []string{"../nas"}}
	if (ResticReport{Source: &hostile}).Validate() == nil {
		t.Error("a hostile target name passed")
	}
}

func TestResticRunReport_PairsEachPathWithItsReasons(t *testing.T) {
	snapshot := strings.Repeat("c", 64)
	valid := []ResticRunReport{
		{Path: ResticPathDirect, Reason: ResticReachable, SnapshotID: snapshot, BytesAdded: 10, DurationSeconds: 7},
		{Path: ResticPathRelay, Reason: ResticTCPUnreachable, TargetHost: "192.0.2.10", TargetPort: 8000},
		{Path: ResticPathRelay, Reason: ResticTLSFailed, TargetHost: "nas", TargetPort: 8000},
		{Path: ResticPathRelay, Reason: ResticTargetNotProvisioned},
		{Path: ResticPathRelay, Reason: ResticRelayForced},
	}
	for _, report := range valid {
		if err := report.Validate(); err != nil {
			t.Errorf("%+v: %v", report, err)
		}
	}
	invalid := []ResticRunReport{
		{Path: ResticPathDirect, Reason: ResticTCPUnreachable},
		{Path: ResticPathRelay, Reason: ResticReachable},
		{Path: "tunnel", Reason: ResticReachable},
		{Path: ResticPathRelay, Reason: "firewall"},
		{Path: ResticPathDirect, Reason: ResticReachable, SnapshotID: "latest"},
		{Path: ResticPathDirect, Reason: ResticReachable, BytesAdded: -1},
		{Path: ResticPathRelay, Reason: ResticTCPUnreachable, TargetHost: "nas:8000"},
		{Path: ResticPathRelay, Reason: ResticTCPUnreachable, TargetPort: 70000},
	}
	for _, report := range invalid {
		if report.Validate() == nil {
			t.Errorf("%+v passed", report)
		}
	}
}

func TestResticRelayRequest_ValidateBoundsTheEvent(t *testing.T) {
	good := ResticRelayRequest{ID: strings.Repeat("a", 32), Repo: "vps", Method: "GET", Path: "/config", ContentLength: 0}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	badID := good
	badID.ID = "../a"
	badRepo := good
	badRepo.Repo = "../nas"
	longQuery := good
	longQuery.Query = strings.Repeat("a", 64)
	newline := good
	newline.Range = "bytes=0-1\r\nX: y"
	for _, request := range []ResticRelayRequest{badID, badRepo, longQuery, newline} {
		if request.Validate() == nil {
			t.Errorf("%+v passed", request)
		}
	}
}

// Les cinq ordres de restic entrent dans la liste fermée des ordres : ils ne
// passent que signés, jamais comme une observation.
func TestResticKinds_AreOrders(t *testing.T) {
	for _, kind := range []Kind{KindResticTargetSetup, KindResticUserAdd, KindResticUserRemove, KindResticProvision, KindResticBackup, KindResticRetain} {
		if !kind.IsOrder() {
			t.Errorf("%s is not an order", kind)
		}
		if _, err := DecodeObservation(string(kind), []byte("{}")); err == nil {
			t.Errorf("%s was read as an observation", kind)
		}
	}
	if KindResticRelay.IsOrder() {
		t.Error("the relay event is not an order")
	}
}

// L'événement du relais a son nom figé, et ses bornes se jugent des deux
// côtés : le serveur n'encode pas, l'agent ne lit pas, une requête hors forme.
func TestResticRelayCommand_IsBoundedOnBothSides(t *testing.T) {
	request := ResticRelayRequest{ID: strings.Repeat("b", 32), Repo: "vps", Method: "POST", Path: "/data/" + strings.Repeat("c", 64), ContentLength: 12}
	event, data, err := ResticRelayCommand(request).Encode()
	if err != nil || event != "restic_relay" {
		t.Fatalf("event %q %v", event, err)
	}
	decoded, err := DecodeObservation(event, data)
	if err != nil || decoded.Relay == nil || *decoded.Relay != request {
		t.Fatalf("decoded %+v %v", decoded, err)
	}
	bad := request
	bad.Repo = "../etc"
	if _, _, err := ResticRelayCommand(bad).Encode(); err == nil {
		t.Error("the server encodes a request outside its bounds")
	}
	if _, err := DecodeObservation(event, []byte(`{"id":"x","repo":"vps","method":"GET","path":"/config","content_length":0}`)); err == nil {
		t.Error("the agent reads a request outside its bounds")
	}
	if _, _, err := (Command{Kind: KindResticRelay}).Encode(); !errors.Is(err, ErrEmptyCommand) {
		t.Errorf("no content: %v", err)
	}
}

// Une section restic hors bornes se refuse par la sentinelle que le serveur
// reconnaît, quelle que soit la règle qui manque.
func TestResticReport_InvalidWrapsTheSentinel(t *testing.T) {
	broken := []ResticReport{
		{Target: &ResticTargetReport{State: "dancing"}},
		{Source: &ResticSourceReport{Targets: []string{"vps/"}}},
		{Target: &ResticTargetReport{State: ResticTargetStopped, RetentionFloor: &ResticRetentionFloor{KeepDaily: -7}}},
	}
	for _, report := range broken {
		if err := report.Validate(); !errors.Is(err, ErrResticReportInvalid) {
			t.Errorf("%+v: %v", report, err)
		}
	}
}

// Les routes du relais vivent sous /agent/ et n'échappent jamais un segment.
func TestResticRelayPaths_StayUnderTheAgentRoutes(t *testing.T) {
	if got := ResticRelayPath("vps"); got != "/agent/relay/rest/vps" {
		t.Errorf("relay %q", got)
	}
	if got := ResticRelayBodyPath("a/b"); got != "/agent/relay/body/a%2Fb" {
		t.Errorf("body %q", got)
	}
	if got := ResticRelayResponsePath("id"); got != "/agent/relay/response/id" {
		t.Errorf("response %q", got)
	}
}

// Le plancher se relève, jamais ne s'abaisse ; une rétention passe s'il
// garde au moins ce qu'il exige, compte par compte.
func TestResticRetentionFloor_RisesButNeverLowers(t *testing.T) {
	floor := DefaultResticRetentionFloor()
	if floor != (ResticRetentionFloor{KeepDaily: 7}) {
		t.Fatalf("default %+v", floor)
	}
	if (ResticRetentionFloor{KeepDaily: 7, KeepWeekly: 4}).IsLowerThan(floor) {
		t.Error("raising the weekly count was taken for lowering")
	}
	for _, lower := range []ResticRetentionFloor{{KeepDaily: 6}, {KeepDaily: 30, KeepWeekly: 3, KeepMonthly: 0}} {
		if !lower.IsLowerThan(ResticRetentionFloor{KeepDaily: 7, KeepWeekly: 4}) {
			t.Errorf("%+v lowers the floor", lower)
		}
	}
	if !floor.Admits(ResticRetainParams{User: "vps", KeepDaily: 7}) {
		t.Error("a policy at the floor was refused")
	}
	for _, below := range []ResticRetainParams{{User: "vps", KeepLast: 1000}, {User: "vps", KeepDaily: 6, KeepWeekly: 52}} {
		if floor.Admits(below) {
			t.Errorf("%+v passed under the floor", below)
		}
	}
}

// Les secrets d'ordre ont une forme fermée, et ne s'écrivent jamais par un
// %v ou un journal.
func TestResticSecrets_HaveAFormAndNeverPrint(t *testing.T) {
	password := strings.Repeat("Ab3_-", 9)[:43]
	secrets := []interface {
		Validate() error
		String() string
	}{
		ResticUserSecret{RESTPassword: password},
		ResticProvisionSecret{RESTPassword: password, RepoPassword: password},
		ResticRetainSecret{RepoPassword: password},
	}
	for _, secret := range secrets {
		if err := secret.Validate(); err != nil {
			t.Errorf("%T: %v", secret, err)
		}
		for _, printed := range []string{fmt.Sprintf("%v", secret), fmt.Sprintf("%+v", secret), fmt.Sprint(secret)} {
			if strings.Contains(printed, password) {
				t.Errorf("%T printed its secret: %s", secret, printed)
			}
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 129), password[:40] + "@:/", password + "\n"} {
		if (ResticRetainSecret{RepoPassword: bad}).Validate() == nil {
			t.Errorf("%q passed", bad)
		}
	}
	if (ResticProvisionSecret{RESTPassword: password}).Validate() == nil {
		t.Error("a provision without the repository password passed")
	}
}
