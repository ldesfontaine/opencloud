package probe

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// forged est une autorité de test et ce qu'elle a signé : de quoi monter
// un serveur TLS sans toucher au réseau.
type forged struct {
	authority *x509.Certificate
	roots     *x509.CertPool
	leaf      tls.Certificate
}

// forge fabrique une autorité et une feuille pour 127.0.0.1, valables
// entre les deux instants donnés. Le nom sert aux cas où la feuille doit
// couvrir autre chose que l'hôte joint.
func forge(t *testing.T, host string, notBefore, notAfter time.Time) forged {
	t.Helper()
	authorityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate authority key: %v", err)
	}
	authorityTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Autorité de test"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	authorityRaw, err := x509.CreateCertificate(rand.Reader, authorityTemplate, authorityTemplate, &authorityKey.PublicKey, authorityKey)
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	authority, err := x509.ParseCertificate(authorityRaw)
	if err != nil {
		t.Fatalf("parse authority: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		leafTemplate.IPAddresses = []net.IP{ip}
	} else {
		leafTemplate.DNSNames = []string{host}
	}
	leafRaw, err := x509.CreateCertificate(rand.Reader, leafTemplate, authority, &leafKey.PublicKey, authorityKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(authority)
	return forged{
		authority: authority,
		roots:     roots,
		leaf:      tls.Certificate{Certificate: [][]byte{leafRaw, authorityRaw}, PrivateKey: leafKey},
	}
}

// serveTLS monte un serveur qui présente la feuille donnée. Le journal du
// serveur part au néant : un refus de chaîne est ce que ces tests
// prouvent, sa trace n'a rien à faire dans la sortie.
func serveTLS(t *testing.T, leaf tls.Certificate) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS12}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func validity() (time.Time, time.Time) {
	return checkNow.Add(-time.Hour), checkNow.AddDate(0, 0, 90)
}

// Le chemin heureux : la chaîne est vérifiée à chaque essai, même quand la
// requête vient d'aboutir. Rien n'est tenu pour vrai sans l'avoir vérifié.
func TestCertificate_TrustedChainIsJudgedOnTheHappyPath(t *testing.T) {
	notBefore, notAfter := validity()
	fake := forge(t, "127.0.0.1", notBefore, notAfter)
	server := serveTLS(t, fake.leaf)

	result := NewChecker(fake.roots).Check(t.Context(), httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeUp || result.Reason != ReasonNone {
		t.Fatalf("attendu en ligne, obtenu %+v", result)
	}
	seen := result.Certificate
	if seen == nil {
		t.Fatal("aucun certificat vu sur une réponse en HTTPS")
	}
	if !seen.ChainValid || !seen.HostnameMatch {
		t.Fatalf("la chaîne de l'autorité connue devait être valide et le nom correspondre: %+v", seen)
	}
	if seen.Expired(checkNow) {
		t.Fatal("un certificat valable 90 jours ne doit pas être dit expiré")
	}
	if !seen.NotAfter.Equal(notAfter.UTC().Truncate(time.Second)) {
		t.Fatalf("échéance attendue %s, obtenue %s", notAfter.UTC(), seen.NotAfter)
	}
	if len(seen.Fingerprint) != 64 {
		t.Fatalf("l'empreinte doit être un SHA-256 en hexadécimal, obtenu %q", seen.Fingerprint)
	}
	if seen.OCSP != OCSPNone {
		t.Fatalf("sans agrafe, l'OCSP ne dit rien, obtenu %q", seen.OCSP)
	}
}

// Une autorité inconnue dégrade l'essai, et surtout laisse les dates
// lisibles : c'est exactement le cas où l'opérateur doit être prévenu de
// l'échéance d'un certificat interne.
func TestCertificate_UnknownAuthorityKeepsTheExpiryReadable(t *testing.T) {
	notBefore, notAfter := validity()
	fake := forge(t, "127.0.0.1", notBefore, notAfter)
	server := serveTLS(t, fake.leaf)

	result := check(httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDegraded || result.Reason != ReasonTLSUntrusted {
		t.Fatalf("attendu dégradé sur autorité inconnue, obtenu %+v", result)
	}
	seen := result.Certificate
	if seen == nil {
		t.Fatal("la poignée de main nue devait rendre la chaîne")
	}
	if seen.ChainValid {
		t.Fatal("une autorité que cette machine ne connaît pas ne valide pas la chaîne")
	}
	if !seen.HostnameMatch {
		t.Fatal("le nom correspond : la confiance et le nom sont deux faits distincts")
	}
	if !seen.NotAfter.Equal(notAfter.UTC().Truncate(time.Second)) {
		t.Fatalf("l'échéance doit rester lisible sur un certificat refusé, obtenue %s", seen.NotAfter)
	}
}

// Un certificat expiré : l'hôte répond parfaitement, seule la confiance
// tombe. L'échéance passée se lit sur le certificat, pas sur l'état.
func TestCertificate_ExpiredIsDegradedNotDown(t *testing.T) {
	fake := forge(t, "127.0.0.1", checkNow.AddDate(0, 0, -90), checkNow.Add(-24*time.Hour))
	server := serveTLS(t, fake.leaf)

	result := NewChecker(fake.roots).Check(t.Context(), httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDegraded || result.Reason != ReasonTLSExpired {
		t.Fatalf("attendu dégradé pour échéance passée, obtenu %+v", result)
	}
	seen := result.Certificate
	if seen == nil {
		t.Fatal("un certificat expiré se rapporte, il ne disparaît pas")
	}
	if !seen.Expired(checkNow) {
		t.Fatalf("l'échéance %s est passée, le certificat doit le dire", seen.NotAfter)
	}
	if seen.ChainValid {
		t.Fatal("une feuille expirée ne fait pas une chaîne valide")
	}
}

// Une chaîne impeccable peut servir le mauvais domaine : les deux faits ne
// se déduisent jamais l'un de l'autre.
func TestCertificate_WrongNameKeepsTheChainValid(t *testing.T) {
	notBefore, notAfter := validity()
	fake := forge(t, "autre.exemple", notBefore, notAfter)
	server := serveTLS(t, fake.leaf)

	result := NewChecker(fake.roots).Check(t.Context(), httpTask(server.URL), checkNow)
	if result.Outcome != OutcomeDegraded || result.Reason != ReasonTLSHostname {
		t.Fatalf("attendu dégradé pour nom qui ne correspond pas, obtenu %+v", result)
	}
	seen := result.Certificate
	if seen == nil {
		t.Fatal("la poignée de main nue devait rendre la chaîne")
	}
	if seen.HostnameMatch {
		t.Fatal("le certificat couvre autre.exemple, pas l'hôte joint")
	}
	if !seen.ChainValid {
		t.Fatal("la chaîne remonte à l'autorité connue : elle est valide, c'est le nom qui ne l'est pas")
	}
	if !strings.Contains(seen.Subject, "autre.exemple") {
		t.Fatalf("le sujet vu doit être celui du certificat, obtenu %q", seen.Subject)
	}
}

// Une sonde TCP qui demande TLS lit le certificat d'un port chiffré qui ne
// parle pas HTTP : c'est ce qui couvre SMTP et IMAP.
func TestCheckTCP_WithTLSReadsTheCertificate(t *testing.T) {
	notBefore, notAfter := validity()
	fake := forge(t, "127.0.0.1", notBefore, notAfter)
	server := serveTLS(t, fake.leaf)
	address := strings.TrimPrefix(server.URL, "https://")

	task := Task{ID: "p3", Kind: KindTCP, Target: address, IntervalSeconds: 60, TimeoutSeconds: 5, TLS: true}
	result := NewChecker(fake.roots).Check(t.Context(), task, checkNow)
	if result.Outcome != OutcomeUp || result.Reason != ReasonNone {
		t.Fatalf("attendu en ligne, obtenu %+v", result)
	}
	if result.Certificate == nil || !result.Certificate.ChainValid {
		t.Fatalf("la poignée de main devait rendre une chaîne valide: %+v", result.Certificate)
	}
	if result.Code != nil {
		t.Fatal("une sonde TCP n'a pas de code HTTP : rien n'a été demandé à l'application")
	}
}

// Sans le drapeau, la même sonde TCP ouvre et referme, sans rien lire.
func TestCheckTCP_WithoutTLSSeesNoCertificate(t *testing.T) {
	notBefore, notAfter := validity()
	fake := forge(t, "127.0.0.1", notBefore, notAfter)
	server := serveTLS(t, fake.leaf)

	task := Task{ID: "p3", Kind: KindTCP, Target: strings.TrimPrefix(server.URL, "https://"), IntervalSeconds: 60, TimeoutSeconds: 5}
	result := NewChecker(fake.roots).Check(t.Context(), task, checkNow)
	if result.Outcome != OutcomeUp {
		t.Fatalf("attendu en ligne, obtenu %+v", result)
	}
	if result.Certificate != nil {
		t.Fatal("sans poignée de main demandée, rien ne doit être rapporté du certificat")
	}
}

// Une sonde TCP qui demande TLS sur un port qui n'en parle pas est hors
// ligne : il n'y a pas de chaîne à juger.
func TestCheckTCP_WithTLSOnAPlainPortIsDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()

	task := Task{ID: "p3", Kind: KindTCP, Target: strings.TrimPrefix(server.URL, "http://"), IntervalSeconds: 60, TimeoutSeconds: 5, TLS: true}
	if result := check(task, checkNow); result.Outcome != OutcomeDown {
		t.Fatalf("attendu hors ligne, obtenu %+v", result)
	}
}

// Le drapeau TLS ne vaut que pour une sonde TCP : l'URL d'une sonde HTTP
// dit déjà le protocole.
func TestDefinition_TLSFlagOnlyAppliesToTCP(t *testing.T) {
	http := Definition{Name: "web", Kind: KindHTTP, Target: "https://exemple.test", MachineID: "m1", TLS: true}.Complete()
	if http.TLS {
		t.Fatal("une sonde HTTP ne porte pas le drapeau : son URL dit le protocole")
	}
	tcp := Definition{Name: "smtp", Kind: KindTCP, Target: "exemple.test:465", MachineID: "m1", TLS: true}.Complete()
	if !tcp.TLS {
		t.Fatal("une sonde TCP garde le drapeau")
	}
}
