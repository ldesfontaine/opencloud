package action

import (
	"crypto/ed25519"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

func openRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

// La clé naît au premier démarrage, en 0600, et se relit la même ensuite.
func TestLoadOrCreateKey_CreatesOnceThenReloads(t *testing.T) {
	root := openRoot(t)
	first, created, err := LoadOrCreateKey(root)
	if err != nil || !created {
		t.Fatalf("first start: created %v, %v", created, err)
	}
	info, err := root.Stat(brand.ServerKeyFile)
	if err != nil || info.Mode().Perm() != keyFileMode {
		t.Fatalf("mode %v %v", info.Mode().Perm(), err)
	}
	again, created, err := LoadOrCreateKey(root)
	if err != nil || created || again.Fingerprint() != first.Fingerprint() {
		t.Fatalf("second start: created %v, %q vs %q, %v", created, again.Fingerprint(), first.Fingerprint(), err)
	}
	if first.Fingerprint() != agentwire.KeyFingerprint(first.PublicKey()) {
		t.Fatal("the fingerprint is not the protocol's")
	}
}

// Les règles de secret.key : lisible par d'autres comptes ou abîmée, la clé
// est refusée, jamais remplacée.
func TestLoadKey_RefusesAnOpenOrDamagedFile(t *testing.T) {
	root := openRoot(t)
	if _, err := LoadKey(root); !errors.Is(err, ErrNoKey) {
		t.Fatalf("missing: %v", err)
	}
	if err := CreateKey(root, GenerateKey()); err != nil {
		t.Fatal(err)
	}
	if err := CreateKey(root, GenerateKey()); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("second create: %v", err)
	}
	if err := root.Chmod(brand.ServerKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(root); !errors.Is(err, ErrKeyFileMode) {
		t.Fatalf("readable by others: %v", err)
	}
	for _, content := range []string{"", "ed25519-v1:\n", "ed25519-v2:AAAA\n", "ed25519-v1:@@@\n", "a\nb\n"} {
		damaged := openRoot(t)
		if err := damaged.WriteFile(brand.ServerKeyFile, []byte(content), keyFileMode); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKey(damaged); !errors.Is(err, ErrKeyFileInvalid) {
			t.Errorf("%q: %v", content, err)
		}
	}
}

// La signature porte sur les octets qui voyagent, précédés de l'étiquette
// des ordres : un corps retouché, ou une signature faite pour autre chose,
// ne se vérifie pas.
func TestSign_SignsTheBytesThatTravel(t *testing.T) {
	key := GenerateKey()
	order := Order{RunID: "r1", MachineID: "m1", AgentID: "a1", Kind: agentwire.KindAgentPing, NotAfter: time.Unix(1700000000, 0), Seq: 7}
	signed, err := key.Sign(order)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Key != key.Fingerprint() || !ed25519.Verify(key.PublicKey(), agentwire.OrderSigningBytes(signed.Body), signed.Signature) {
		t.Fatal("a valid order does not verify")
	}
	if want := `{"run_id":"r1","machine_id":"m1","agent_id":"a1","kind":"agent.ping","not_after":1700000000,"seq":7}`; string(signed.Body) != want {
		t.Fatalf("body %s", signed.Body)
	}
	tampered := append([]byte(nil), signed.Body...)
	tampered[len(tampered)-2] = '8'
	if ed25519.Verify(key.PublicKey(), agentwire.OrderSigningBytes(tampered), signed.Signature) {
		t.Fatal("a tampered body verifies")
	}
	if ed25519.Verify(key.PublicKey(), signed.Body, signed.Signature) {
		t.Fatal("the signature holds without its domain label")
	}
}
