package restic

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/testkit"
)

// selfSigned tire un certificat auto-signé ECDSA P-256 pour 127.0.0.1, comme
// celui que l'agent de la cible tire pour rest-server.
func selfSigned(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "nas"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"nas"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, certificatePEM
}

// listenTLS ouvre une cible TLS sur la boucle locale qui termine chaque
// poignée de main ; elle rend son port.
func listenTLS(t *testing.T, certificate tls.Certificate) int {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestChoose_TakesTheDirectPathWhenTheTargetAnswersWithThePinnedCertificate(t *testing.T) {
	certificate, certificatePEM := selfSigned(t)
	port := listenTLS(t, certificate)
	target := Target{Host: "127.0.0.1", Port: port, CertificatePEM: certificatePEM}

	choice := Choose(context.Background(), &net.Dialer{}, target, false)

	if choice.Path != agentwire.ResticPathDirect || choice.Reason != agentwire.ResticReachable {
		t.Fatalf("choice %+v", choice)
	}
	if choice.Hint != (Hint{}) {
		t.Errorf("a direct path needs no hint: %+v", choice.Hint)
	}
}

func TestChoose_FallsBackToTheRelayWhenTheTargetPortIsClosed(t *testing.T) {
	_, certificatePEM := selfSigned(t)
	target := Target{Host: "127.0.0.1", Port: testkit.ClosedPort(t), CertificatePEM: certificatePEM}

	choice := Choose(context.Background(), &net.Dialer{}, target, false)

	if choice.Path != agentwire.ResticPathRelay || choice.Reason != agentwire.ResticTCPUnreachable {
		t.Fatalf("choice %+v", choice)
	}
}

func TestChoose_FallsBackToTheRelayWhenTheCertificateIsNotThePinnedOne(t *testing.T) {
	served, _ := selfSigned(t)
	_, pinnedPEM := selfSigned(t)
	target := Target{Host: "127.0.0.1", Port: listenTLS(t, served), CertificatePEM: pinnedPEM}

	choice := Choose(context.Background(), &net.Dialer{}, target, false)

	if choice.Path != agentwire.ResticPathRelay || choice.Reason != agentwire.ResticTLSFailed {
		t.Fatalf("choice %+v", choice)
	}
}

// Le certificat est le bon, mais l'hôte annoncé n'est pas dans ses noms :
// restic refuserait, la sonde aussi.
func TestChoose_FallsBackToTheRelayWhenTheAnnouncedHostIsNotInTheCertificate(t *testing.T) {
	certificate, certificatePEM := selfSigned(t)
	port := listenTLS(t, certificate)
	dialer := redirectDialer{to: "127.0.0.1:" + strconv.Itoa(port)}
	target := Target{Host: "vps", Port: port, CertificatePEM: certificatePEM}

	choice := Choose(context.Background(), dialer, target, false)

	if choice.Reason != agentwire.ResticTLSFailed {
		t.Fatalf("choice %+v", choice)
	}
}

// redirectDialer compose toujours la même adresse : un nom annoncé arrive
// sur la boucle locale sans résolveur.
type redirectDialer struct {
	to string
}

func (d redirectDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, d.to)
}

// recordingDialer note l'adresse demandée et refuse : aucun réseau.
type recordingDialer struct {
	asked *string
}

func (d recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	*d.asked = address
	return nil, errors.New("connection refused")
}

func TestChoose_TheHintCarriesTheExactTargetHostAndPort(t *testing.T) {
	_, certificatePEM := selfSigned(t)
	var asked string
	target := Target{Host: "192.0.2.10", Port: 8443, CertificatePEM: certificatePEM}

	choice := Choose(context.Background(), recordingDialer{asked: &asked}, target, false)

	if choice.Hint != (Hint{TargetHost: "192.0.2.10", TargetPort: 8443}) {
		t.Fatalf("hint %+v", choice.Hint)
	}
	if asked != "192.0.2.10:8443" {
		t.Errorf("probed %q", asked)
	}
	report := choice.Report()
	if report.Validate() != nil || report.TargetHost != "192.0.2.10" || report.TargetPort != 8443 || report.Reason != agentwire.ResticTCPUnreachable {
		t.Errorf("report %+v", report)
	}
}

func TestChoose_GoesThroughTheRelayWhenTheTargetIsNotProvisioned(t *testing.T) {
	var asked string

	choice := Choose(context.Background(), recordingDialer{asked: &asked}, Target{}, false)

	if choice.Path != agentwire.ResticPathRelay || choice.Reason != agentwire.ResticTargetNotProvisioned {
		t.Fatalf("choice %+v", choice)
	}
	if asked != "" {
		t.Errorf("nothing to probe, yet dialed %q", asked)
	}
}

func TestChoose_ForcedRelaySkipsTheProbe(t *testing.T) {
	var asked string
	target := Target{Host: "nas", Port: 8000, CertificatePEM: "x"}

	choice := Choose(context.Background(), recordingDialer{asked: &asked}, target, true)

	if choice.Path != agentwire.ResticPathRelay || choice.Reason != agentwire.ResticRelayForced || asked != "" {
		t.Fatalf("choice %+v, dialed %q", choice, asked)
	}
}
