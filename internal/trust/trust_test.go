package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_WithoutPath_LeavesTheSystemStoreAlone(t *testing.T) {
	roots, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// nil est porteur : crypto/tls et crypto/x509 le lisent « magasin du
	// système ». Un jeu vide, lui, refuserait toutes les chaînes.
	if roots != nil {
		t.Fatal("an empty path must give nil, not a pool")
	}
}

func TestLoad_AddsTheAuthorityWithoutDroppingTheSystemOnes(t *testing.T) {
	authority, key := selfSignedAuthority(t)
	path := writePEM(t, authority.Raw)

	roots, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if roots == nil {
		t.Fatal("a readable bundle must give a pool")
	}
	leaf := signedBy(t, authority, key, "service.interne")
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "service.interne"}); err != nil {
		t.Fatalf("the added authority must validate its own leaf: %v", err)
	}
	// Le magasin du système est le point de départ, pas une victime : un
	// jeu fabriqué de zéro couperait toutes les autorités publiques.
	alone := x509.NewCertPool()
	alone.AddCert(authority)
	if roots.Equal(alone) {
		t.Fatal("the bundle must be added to the system roots, not replace them")
	}
}

func TestLoad_MissingFile_StopsTheStart(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Fatal("a missing ca file must be an error, never an empty store")
	}
}

func TestLoad_FileWithoutCertificate_StopsTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, []byte("ceci n'est pas un certificat\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a bundle without any certificate must be an error")
	}
}

func selfSignedAuthority(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Autorité interne de test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	authority, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse authority: %v", err)
	}
	return authority, key
}

func signedBy(t *testing.T, authority *x509.Certificate, key *ecdsa.PrivateKey, host string) *x509.Certificate {
	t.Helper()
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, authority, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	leaf, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return leaf
}

func writePEM(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	return path
}
