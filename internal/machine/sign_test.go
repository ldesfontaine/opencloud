package machine

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// Le protocole 2 signe l'étiquette, l'aléa, l'identifiant précédé de sa
// longueur et l'horodatage : octet pour octet, figé. La machine openCloud
// (« local ») signe comme les autres.
func TestSignedPayload_IsByteExact(t *testing.T) {
	nonce := bytes.Repeat([]byte{7}, NonceSize)
	payload, err := SignedPayload(nonce, "local", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte("opencloud-agent-proof/v2\x00"), nonce...)
	want = append(want, 0, 0, 0, 5)
	want = append(want, "local"...)
	want = append(want, 0, 0, 0, 0, 0, 0, 0, 1)
	if !bytes.Equal(payload, want) {
		t.Fatalf("payload %x\nwant    %x", payload, want)
	}
	if _, err := SignedPayload(nonce[:5], "local", 1); !errors.Is(err, ErrBadProof) {
		t.Errorf("short nonce: %v", err)
	}
	if _, err := SignedPayload(nonce, "", 1); !errors.Is(err, ErrBadID) {
		t.Errorf("empty id: %v", err)
	}
	if _, err := SignedPayload(nonce, strings.Repeat("a", 65), 1); !errors.Is(err, ErrBadID) {
		t.Errorf("long id: %v", err)
	}
}

func TestVerify_DoesNotPanicOnAMalformedKey(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	payload := []byte("x")
	signature := Sign(private, payload)
	if !Verify(public, payload, signature) {
		t.Fatal("valid signature refused")
	}
	if Verify([]byte("short"), payload, signature) || Verify(nil, payload, signature) {
		t.Fatal("malformed key accepted")
	}
}

func TestNewID_IsAVersion4UUID(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 || id[14] != '4' || ValidateID(id) != nil {
		t.Fatalf("id %q", id)
	}
}
