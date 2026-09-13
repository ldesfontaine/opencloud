package machine

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignedPayload_IsByteExact(t *testing.T) {
	nonce := make([]byte, NonceSize)
	payload, err := SignedPayload(nonce, "01234567-89ab-cdef-0123-456789abcdef", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != signedPayloadSize || payload[signedPayloadSize-1] != 1 || payload[NonceSize] != 0x01 {
		t.Fatalf("payload %x", payload)
	}
	if _, err := SignedPayload(nonce[:5], "01234567-89ab-cdef-0123-456789abcdef", 1); err == nil {
		t.Error("short nonce accepted")
	}
	if _, err := SignedPayload(nonce, "local", 1); err == nil {
		t.Error("non-uuid accepted")
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
