package anchor

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Un type d'ancre ajouté ou retiré du registre sans mettre à jour cette liste
// fait échouer le test : la décision d'élargir ce que l'agent peut refuser (ou
// de ne plus le refuser) se lit dans le diff de ce fichier.
func TestKinds_AreTheExpectedOnes(t *testing.T) {
	want := []string{"backup.recipient", "backup.guest", "restic.target-cert"}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("anchor kinds %v, expected %v: update this list on purpose", got, want)
	}
}

func TestKinds_AreWellDeclared(t *testing.T) {
	flags, keys := map[string]bool{}, map[string]bool{}
	for _, kind := range Kinds() {
		if kind.Flag == "" || kind.Right == "" || kind.Max < 1 || kind.Valid == nil || len(kind.Bound) == 0 {
			t.Errorf("%s: incomplete declaration", kind.Name)
		}
		if flags[kind.Flag] {
			t.Errorf("%s: flag %q already used", kind.Name, kind.Flag)
		}
		flags[kind.Flag] = true
		for _, refused := range []*refusal.Error{kind.Missing, kind.Outside, kind.Unreadable} {
			key, ok := refusal.KeyOf(refused)
			if !ok || !strings.HasPrefix(key, "anchor.") || keys[key] {
				t.Errorf("%s: refusal key %q missing or shared", kind.Name, key)
			}
			keys[key] = true
		}
	}
}

func testRecipient(t *testing.T) string {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return identity.Recipient().String()
}

func testCertificate(t *testing.T) (certificatePEM, fingerprint string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "restic-target"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, ok := fingerprintOf(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))
	if !ok {
		t.Fatal("fingerprint")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), fingerprint
}

func produceParams(recipient, target string) json.RawMessage {
	params, _ := json.Marshal(map[string]string{"producer": "proxmox-vzdump", "target": target, "recipient": recipient})
	return params
}

func TestJudge_BackupProduce(t *testing.T) {
	trusted, other := testRecipient(t), testRecipient(t)
	state := FromFile(map[string][]string{BackupRecipient: {trusted}, BackupGuest: {"101", "102"}})
	cases := []struct {
		name    string
		state   State
		params  json.RawMessage
		kind    string
		why     Why
		command string
	}{
		{"trusted recipient and guest", state, produceParams(trusted, "101"), "", "", ""},
		{"recipient out of the anchors", state, produceParams(other, "101"), BackupRecipient, WhyOutside, "agent trust add backup.recipient " + other},
		{"guest out of the anchors", state, produceParams(trusted, "103"), BackupGuest, WhyOutside, "agent trust add backup.guest 103"},
		{"no anchor at all", FromFile(nil), produceParams(trusted, "101"), BackupRecipient, WhyMissing, "agent trust add backup.recipient " + trusted},
		{"guest anchor missing", FromFile(map[string][]string{BackupRecipient: {trusted}}), produceParams(trusted, "101"), BackupGuest, WhyMissing, "agent trust add backup.guest 101"},
		{"unreadable list", FromFile(map[string][]string{BackupRecipient: {"not-an-age-key"}, BackupGuest: {"101"}}), produceParams(trusted, "101"), BackupRecipient, WhyUnreadable, "agent trust add backup.recipient " + trusted},
		{"parameters that do not read", state, json.RawMessage(`{"target":`), BackupRecipient, WhyOutside, ""},
		{"a guest with a leading zero is not 101", state, produceParams(trusted, "0101"), BackupGuest, WhyOutside, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			finding := Judge(c.state, "backup.produce", c.params)
			if c.kind == "" {
				if finding != nil {
					t.Fatalf("refused: %+v", finding)
				}
				return
			}
			if finding == nil || finding.Kind.Name != c.kind || finding.Why != c.why {
				t.Fatalf("finding %+v, expected %s %s", finding, c.kind, c.why)
			}
			if got := finding.Command(); !strings.HasSuffix(got, c.command) {
				t.Fatalf("command %q, expected a suffix %q", got, c.command)
			}
			if _, ok := refusal.KeyOf(finding.Refusal()); !ok {
				t.Fatal("the refusal carries no key")
			}
		})
	}
}

func TestJudge_ResticProvisionJudgesTheCertificateFingerprint(t *testing.T) {
	certificatePEM, fingerprint := testCertificate(t)
	params, _ := json.Marshal(map[string]any{"target": "t", "host": "10.0.0.2", "port": 8000, "user": "u", "certificate": certificatePEM})
	if finding := Judge(FromFile(map[string][]string{ResticTargetCert: {fingerprint}}), "restic.provision", params); finding != nil {
		t.Fatalf("anchored certificate refused: %+v", finding)
	}
	elsewhere := strings.Repeat("ab", 32)
	finding := Judge(FromFile(map[string][]string{ResticTargetCert: {elsewhere}}), "restic.provision", params)
	if finding == nil || finding.Why != WhyOutside || finding.Value != fingerprint {
		t.Fatalf("foreign certificate: %+v", finding)
	}
	if finding := Judge(FromFile(nil), "restic.provision", params); finding == nil || finding.Why != WhyMissing {
		t.Fatalf("no anchor: %+v", finding)
	}
	notACertificate, _ := json.Marshal(map[string]string{"certificate": "-----BEGIN PRIVATE KEY-----"})
	if finding := Judge(FromFile(map[string][]string{ResticTargetCert: {fingerprint}}), "restic.provision", notACertificate); finding == nil {
		t.Fatal("something that is not a certificate must not pass")
	}
}

func TestJudge_OtherOrdersAreNotBounded(t *testing.T) {
	if finding := Judge(FromFile(nil), "agent.ping", nil); finding != nil {
		t.Fatalf("ping refused: %+v", finding)
	}
}

func TestJudgeReported_AnAgentThatSaysNothingIsNotJudged(t *testing.T) {
	if finding := JudgeReported(State{}, "backup.produce", produceParams("x", "1")); finding != nil {
		t.Fatalf("a v0.6.0 agent applies no anchor, the server must not refuse for it: %+v", finding)
	}
	if finding := JudgeReported(FromFile(nil), "backup.produce", produceParams("x", "1")); finding == nil {
		t.Fatal("an agent that reports no anchor refuses: the server knows it")
	}
}

func TestFromFile_ReadsWhatIsValidAndMarksTheRest(t *testing.T) {
	recipient := testRecipient(t)
	tooMany := []string{"101", "102", "103", "104", "105", "106", "107", "108", "109", "110", "111", "112", "113", "114", "115", "116", "117", "118", "119", "120", "121", "122", "123", "124", "125", "126", "127", "128", "129", "130", "131", "132", "133"}
	state := FromFile(map[string][]string{
		BackupRecipient:  {recipient, recipient},
		BackupGuest:      tooMany,
		ResticTargetCert: {strings.Repeat("0", 64)},
		"future.kind":    {"x"},
	})
	if !state.Unreadable[BackupRecipient] || !state.Unreadable[BackupGuest] {
		t.Fatalf("a duplicate and an overflow must be unreadable: %+v", state)
	}
	if len(state.Values[ResticTargetCert]) != 1 || len(state.Values[BackupRecipient]) != 0 {
		t.Fatalf("values %+v", state.Values)
	}
	if got := FromFile(nil); got.HasAny() || !got.Reported {
		t.Fatalf("empty file: %+v", got)
	}
}

func TestFormatAndParse_RoundTrip(t *testing.T) {
	recipient := testRecipient(t)
	state := FromFile(map[string][]string{BackupRecipient: {recipient}, BackupGuest: {"101", "102"}, ResticTargetCert: {"zz"}})
	text := state.Format()
	if !strings.HasPrefix(text, "backup.recipient="+recipient+",backup.guest=101:102,") || !strings.HasSuffix(text, "restic.target-cert=!") {
		t.Fatalf("text %q", text)
	}
	parsed, err := Parse(text)
	if err != nil || parsed.Format() != text || !parsed.Reported {
		t.Fatalf("parsed %+v: %v", parsed, err)
	}
	none, err := Parse(FromFile(nil).Format())
	if err != nil || !none.Reported || none.HasAny() {
		t.Fatalf("none %+v: %v", none, err)
	}
	silent, err := Parse("")
	if err != nil || silent.Reported {
		t.Fatalf("silent %+v: %v", silent, err)
	}
}

func TestParse_RefusesAnythingItDoesNotKnow(t *testing.T) {
	for _, text := range []string{
		"backup.guest", "unknown.kind=1", "backup.guest=abc", "backup.guest=101:101", "backup.guest=101,backup.guest=102",
		"backup.guest=!,backup.guest=!", "backup.recipient=age1xyz", strings.Repeat("a", maxHeaderBytes+1),
	} {
		if _, err := Parse(text); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%q: %v", text, err)
		}
	}
}

func TestAddAndRemove_KeepTheBounds(t *testing.T) {
	guest, _ := Lookup(BackupGuest)
	stored, err := Add(nil, guest, "101")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Add(stored, guest, "101")
	if err != nil || len(again[BackupGuest]) != 1 {
		t.Fatalf("a duplicate is not added twice: %v %v", again, err)
	}
	if _, err := Add(stored, guest, "10 1"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("bad value: %v", err)
	}
	for number := 102; number < 101+guest.Max; number++ {
		if stored, err = Add(stored, guest, strconv.Itoa(number)); err != nil {
			t.Fatal(err)
		}
	}
	if len(stored[BackupGuest]) != guest.Max {
		t.Fatalf("expected %d guests, got %d", guest.Max, len(stored[BackupGuest]))
	}
	if _, err := Add(stored, guest, "999"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("full: %v", err)
	}
	if _, err := Add(map[string][]string{BackupGuest: {"nope"}}, guest, "2"); !errors.Is(err, ErrStoredUnreadable) {
		t.Fatalf("stored unreadable: %v", err)
	}
	removed, err := Remove(stored, guest, "101")
	if err != nil || slices.Contains(removed[BackupGuest], "101") {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if _, err := Remove(removed, guest, "101"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("absent: %v", err)
	}
	fixed, err := Remove(map[string][]string{BackupGuest: {"nope"}}, guest, "nope")
	if err != nil || len(fixed) != 0 {
		t.Fatalf("an unreadable value must be removable: %v %v", fixed, err)
	}
}

func TestRefused_CarriesTheKeyAndTheCommand(t *testing.T) {
	finding := Judge(FromFile(nil), "backup.produce", produceParams("age1x", "101"))
	if finding == nil {
		t.Fatal("expected a finding")
	}
	var err error = &Refused{Finding: *finding}
	key, ok := refusal.KeyOf(err)
	if !ok || key != "anchor.backup_recipient_missing" || !refusal.IsConflict(err) {
		t.Fatalf("key %q %v", key, ok)
	}
	var refused *Refused
	if !errors.As(err, &refused) || refused.Command() != "" {
		t.Fatalf("a recipient that is not valid has no command: %q", refused.Command())
	}
}
