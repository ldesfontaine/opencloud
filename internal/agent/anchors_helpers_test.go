package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/anchor"
)

// trust pose des ancres dans le fichier d'identité du banc, comme le fait
// l'administrateur de la machine : l'agent les relit avant chaque ordre.
func (b *orderBench) trust(t *testing.T, anchors map[string][]string) {
	t.Helper()
	_, err := UpdateIdentity(b.stateDir, nil, func(identity *Identity) error {
		merged := anchor.Clone(identity.Anchors)
		for name, values := range anchors {
			merged[name] = values
		}
		identity.Anchors = merged
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fingerprintOfPEM est l'empreinte que l'ancre restic.target-cert attend ; vide
// pour un texte qui n'est pas un PEM.
func fingerprintOfPEM(certificatePEM string) string {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
}

// storedAnchors lit les ancres du fichier d'identité du banc.
func (b *orderBench) storedAnchors(t *testing.T) anchor.State {
	t.Helper()
	identity, err := LoadIdentity(b.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return identity.AnchorState()
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
