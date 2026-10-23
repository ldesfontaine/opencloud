package release

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Les vecteurs viennent de py-minisign 0.21.1, une mise en œuvre de minisign
// qui n'a rien de commun avec ce paquet : la compatibilité ne se prouve pas
// avec soi-même. Les clés privées ne sont jamais écrites ici.
const (
	vectorSums = "3f1c0e5a7b0c4f4d8e2a9b6c1d7e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e  opencloud-linux-amd64\n" +
		"9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b  opencloud-linux-arm64\n"
	vectorPublicKey      = "RWQoyFu5HrOe4lYlrd5JyPz8VGMODuhoMhFTQ4Pn9oyS+d1WuxTgQr2C"                                             // gitleaks:allow
	vectorOtherPublicKey = "RWSAcGD7RKg7cDFHD0vM4oCuy2VdfIeKzzOOWF92ZrTKzwhB14pTlF7Q"                                             // gitleaks:allow
	vectorSignatureLine  = "RUQoyFu5HrOe4jY85hMC3943NcSZaKu2GCeoT4AnrJTyps6NhJ0Td9gHYEGyBzHMq60nCGjqPCDfgHXE8tKwDVc5d0008pCvQws=" // gitleaks:allow
	vectorGlobalLine     = "FRUKBtKroX7ed1D4dCNr2aSIhSMagAOGJoQZOG4kWP2zjqWwqLfkSbbERz1+08GJMLYnGFXtUDcgK9yorRXnBQ=="             // gitleaks:allow
	vectorComment        = "timestamp:1792540800\tfile:SHA256SUMS\tversion:v0.0.2"
)

func vectorSignature() string {
	return "untrusted comment: signature from opencloud release key\n" +
		vectorSignatureLine + "\n" +
		"trusted comment: " + vectorComment + "\n" +
		vectorGlobalLine + "\n"
}

func mustPublicKey(t *testing.T, text string) PublicKey {
	t.Helper()
	key, err := ParsePublicKey(text)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestVerify_AcceptsASignatureMadeByMinisign(t *testing.T) {
	comment, err := Verify([]byte(vectorSums), []byte(vectorSignature()), mustPublicKey(t, vectorPublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if comment != vectorComment {
		t.Fatalf("trusted comment %q", comment)
	}
}

func TestVerify_RefusesAChangedMessage(t *testing.T) {
	changed := strings.Replace(vectorSums, "3f1c", "3f1d", 1)
	_, err := Verify([]byte(changed), []byte(vectorSignature()), mustPublicKey(t, vectorPublicKey))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("changed message: %v", err)
	}
}

func TestVerify_RefusesAChangedTrustedComment(t *testing.T) {
	changed := strings.Replace(vectorSignature(), "version:v0.0.2", "version:v0.0.3", 1)
	_, err := Verify([]byte(vectorSums), []byte(changed), mustPublicKey(t, vectorPublicKey))
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("changed comment: %v", err)
	}
}

func TestVerify_RefusesAnotherKey(t *testing.T) {
	_, err := Verify([]byte(vectorSums), []byte(vectorSignature()), mustPublicKey(t, vectorOtherPublicKey))
	if !errors.Is(err, ErrWrongKey) {
		t.Fatalf("another key: %v", err)
	}
}

func TestVerify_RefusesTheLegacyAlgorithm(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(vectorSignatureLine)
	if err != nil {
		t.Fatal(err)
	}
	copy(raw, "Ed")
	legacy := strings.Replace(vectorSignature(), vectorSignatureLine, base64.StdEncoding.EncodeToString(raw), 1)
	_, err = Verify([]byte(vectorSums), []byte(legacy), mustPublicKey(t, vectorPublicKey))
	if !errors.Is(err, ErrLegacyAlgorithm) {
		t.Fatalf("legacy algorithm: %v", err)
	}
}

func TestVerify_RefusesMalformedFiles(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(vectorSignature(), "\n"), "\n")
	join := func(lines ...string) string { return strings.Join(lines, "\n") + "\n" }
	cases := map[string]string{
		"three lines":          join(lines[0], lines[1], lines[2]),
		"five lines":           join(lines[0], lines[1], lines[2], lines[3], "extra"),
		"invalid base64":       join(lines[0], "not base64 !", lines[2], lines[3]),
		"signature too short":  join(lines[0], vectorSignatureLine[:40], lines[2], lines[3]),
		"global too short":     join(lines[0], lines[1], lines[2], vectorGlobalLine[:40]),
		"no untrusted prefix":  join("signature from a key", lines[1], lines[2], lines[3]),
		"no trusted prefix":    join(lines[0], lines[1], vectorComment, lines[3]),
		"unknown algorithm":    join(lines[0], "RVEoyFu5HrOe4jY85hMC3943NcSZaKu2GCeoT4AnrJTyps6NhJ0Td9gHYEGyBzHMq60nCGjqPCDfgHXE8tKwDVc5d0008pCvQws=", lines[2], lines[3]),
		"windows line endings": strings.ReplaceAll(vectorSignature(), "\n", "\r\n"),
		"empty":                "",
	}
	key := mustPublicKey(t, vectorPublicKey)
	for name, file := range cases {
		if _, err := Verify([]byte(vectorSums), []byte(file), key); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVerify_RefusesAnEmptyKey(t *testing.T) {
	if _, err := Verify([]byte(vectorSums), []byte(vectorSignature()), PublicKey{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty key: %v", err)
	}
}

func TestSign_ThenVerify_RoundTrip(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	file, err := Sign([]byte(vectorSums), privateKey, vectorComment)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(file), "untrusted comment: signature from ") || strings.Count(string(file), "\n") != 4 || !strings.HasSuffix(string(file), "\n") {
		t.Fatalf("signature file: %q", file)
	}
	comment, err := Verify([]byte(vectorSums), file, publicKey)
	if err != nil || comment != vectorComment {
		t.Fatalf("verify: %q %v", comment, err)
	}
	otherPublic, otherPrivate, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify([]byte(vectorSums), file, otherPublic); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("another generated key: %v", err)
	}
	if _, err := Sign([]byte(vectorSums), otherPrivate, "two\nlines"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("comment on two lines: %v", err)
	}
	if _, err := Sign([]byte(vectorSums), PrivateKey{}, vectorComment); !errors.Is(err, ErrMalformed) {
		t.Fatalf("empty private key: %v", err)
	}
}

func TestPrivateKey_EncodeParsesBack(t *testing.T) {
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePrivateKey(privateKey.Encode() + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Encode() != privateKey.Encode() {
		t.Fatal("private key changed")
	}
	file, err := Sign([]byte("message"), parsed, "timestamp:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify([]byte("message"), file, publicKey); err != nil {
		t.Fatalf("signature of the parsed key: %v", err)
	}
	for _, text := range []string{"", "not base64 !", base64.StdEncoding.EncodeToString(make([]byte, 39))} {
		if _, err := ParsePrivateKey(text); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: %v", text, err)
		}
	}
	if _, err := ParsePrivateKey("a-secret-that-must-not-be-echoed"); err == nil || strings.Contains(err.Error(), "echoed") {
		t.Fatalf("error repeats the text: %v", err)
	}
}

// Une clé privée passée par erreur à fmt ou à un journal ne montre rien
// d'elle : ni la base64 du secret, ni les octets de la graine.
func TestPrivateKey_FormatNeverShowsTheKey(t *testing.T) {
	_, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encoded := privateKey.Encode()
	seed := privateKey.key.Seed()
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		for _, value := range []any{privateKey, &privateKey, struct{ Key PrivateKey }{privateKey}} {
			text := fmt.Sprintf(verb, value)
			if strings.Contains(text, encoded) || strings.Contains(text, hex.EncodeToString(seed)) ||
				strings.Contains(text, strings.Trim(fmt.Sprint(seed[:3]), "[]")) || !strings.Contains(text, redactedPrivateKey) {
				t.Errorf("%s of %T shows the key: %s", verb, value, text)
			}
		}
	}
}

// Même identifiant, autre clé : l'identifiant ne sert qu'à dire « pas la
// bonne clé », la signature reste vérifiée.
func TestVerify_RefusesAKeyWithTheSameIDButOtherBytes(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(vectorPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	forged := mustPublicKey(t, base64.StdEncoding.EncodeToString(raw))
	if _, err := Verify([]byte(vectorSums), []byte(vectorSignature()), forged); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("same id, other key: %v", err)
	}
}

func TestParsePublicKey_AcceptsTheTwoLineFile(t *testing.T) {
	file := "untrusted comment: minisign public key 8DD7C9D4F4E8A5B3\n" + vectorPublicKey + "\n"
	fromFile := mustPublicKey(t, file)
	if fromFile.String() != vectorPublicKey || mustPublicKey(t, vectorPublicKey+"\n").String() != vectorPublicKey {
		t.Fatalf("public key does not round-trip: %s", fromFile)
	}
	for _, text := range []string{"", "a\nb\nc", "no comment\n" + vectorPublicKey, "!!!!", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 42))} {
		if _, err := ParsePublicKey(text); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: %v", text, err)
		}
	}
}
