package tlsinspect

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/egress"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// Un pare-feu qui filtre ne répond pas : on attend assez pour le dire,
	// pas assez pour que l'opérateur croie l'écran figé.
	inspectTimeout = 8 * time.Second
	maxURLLength   = 2048
)

// trustedRoots : nil, le magasin du système juge ; les tests posent le leur.
var trustedRoots *x509.CertPool

var ErrURLInvalid = refusal.Invalid("certificate.url_invalid", "the address must look like https://host[:port], without a path")

// Outcome dit ce que l'inspection a trouvé.
type Outcome string

const (
	// Trusted : joint, et le magasin du système reconnaît le certificat pour
	// cet hôte. L'empreinte est donnée, mais rien n'oblige à l'épingler.
	Trusted Outcome = "trusted"
	// Untrusted : joint, mais le certificat n'est pas reconnu (auto-signé,
	// autorité inconnue, autre nom). Seule l'empreinte, confirmée par
	// l'opérateur, permet de s'y fier.
	Untrusted Outcome = "untrusted"
	// Unreachable : pas de handshake ; Cause dit pourquoi.
	Unreachable Outcome = "unreachable"
)

// Cause d'un échec de connexion, du plus au moins parlant pour l'opérateur.
const (
	// CauseTimeout : rien ne répond, le signe d'un filtrage (règle de
	// pare-feu manquante, mauvaise interface).
	CauseTimeout = "timeout"
	// CauseRefused : la machine répond, mais rien n'écoute sur ce port.
	CauseRefused = "refused"
	// CauseUnresolved : le nom n'est pas résolu par la machine openCloud.
	CauseUnresolved = "unresolved"
	// CauseNoRoute : la machine openCloud n'a pas de route vers l'adresse.
	CauseNoRoute = "no_route"
	// CauseForbidden : adresse qu'openCloud ne joint jamais (lien-local).
	CauseForbidden = "forbidden"
	// CauseNotTLS : quelque chose répond, mais pas en TLS (port http).
	CauseNotTLS = "not_tls"
	CauseOther  = "other"
)

// Report est le relevé d'une adresse.
type Report struct {
	Outcome Outcome
	// Cause : seulement pour Unreachable.
	Cause string
	// Fingerprint : SHA-256 du certificat présenté, en hexadécimal minuscule
	// sans séparateur ; vide pour Unreachable.
	Fingerprint string
	// Subject : le nom commun (ou, à défaut, le sujet entier) du certificat.
	Subject  string
	NotAfter time.Time
}

// Inspect joint l'adresse et relève son certificat. Joignable ou non, ce
// n'est pas une erreur : c'est la réponse. Seule une adresse mal formée est
// refusée (ErrURLInvalid).
func Inspect(ctx context.Context, rawURL string) (Report, error) {
	host, port, err := splitAddress(rawURL)
	if err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()

	connection, err := egress.DialWith(ctx, "tcp", net.JoinHostPort(host, port), inspectTimeout, true)
	if err != nil {
		return Report{Outcome: Unreachable, Cause: causeOf(err)}, nil
	}
	defer connection.Close()

	// La chaîne est jugée à la main, après le handshake : le but est de relever
	// le certificat même quand le système ne le reconnaît pas.
	config := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true} // #nosec G402 -- relevé de l'empreinte, rien n'est envoyé sur ce canal.
	if net.ParseIP(host) == nil {
		config.ServerName = host
	}
	client := tls.Client(connection, config)
	if err := client.HandshakeContext(ctx); err != nil {
		return Report{Outcome: Unreachable, Cause: handshakeCauseOf(err)}, nil
	}
	chain := client.ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return Report{Outcome: Unreachable, Cause: CauseNotTLS}, nil
	}
	leaf := chain[0]
	sum := sha256.Sum256(leaf.Raw)
	report := Report{
		Outcome:     Untrusted,
		Fingerprint: hex.EncodeToString(sum[:]),
		Subject:     subjectOf(leaf),
		NotAfter:    leaf.NotAfter.UTC(),
	}
	if isRecognized(leaf, chain[1:], host) {
		report.Outcome = Trusted
	}
	return report, nil
}

// splitAddress lit https://hôte[:port] (443 par défaut). Même forme que
// l'adresse des déclarations : ni identifiants, ni chemin, ni requête.
func splitAddress(raw string) (host, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLength {
		return "", "", ErrURLInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", "", ErrURLInvalid
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", "", ErrURLInvalid
	}
	port = parsed.Port()
	if port == "" {
		port = "443"
	}
	return parsed.Hostname(), port, nil
}

// isRecognized : la chaîne remonte à une autorité du système, et le certificat
// vaut pour l'hôte (VerifyHostname connaît aussi les adresses IP).
func isRecognized(leaf *x509.Certificate, rest []*x509.Certificate, host string) bool {
	intermediates := x509.NewCertPool()
	for _, certificate := range rest {
		intermediates.AddCert(certificate)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: trustedRoots, Intermediates: intermediates}); err != nil {
		return false
	}
	return leaf.VerifyHostname(host) == nil
}

func subjectOf(certificate *x509.Certificate) string {
	if certificate.Subject.CommonName != "" {
		return certificate.Subject.CommonName
	}
	return certificate.Subject.String()
}

func causeOf(err error) string {
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.Is(err, egress.ErrForbiddenAddress), errors.Is(err, egress.ErrPrivateAddress):
		return CauseForbidden
	case errors.As(err, &dns):
		return CauseUnresolved
	case errors.Is(err, syscall.ECONNREFUSED):
		return CauseRefused
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return CauseNoRoute
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		return CauseTimeout
	}
	return CauseOther
}

// handshakeCauseOf : le handshake a échoué après une connexion établie. Un
// délai reste un délai (un filtre qui accepte puis se tait) ; le reste est
// un service qui ne parle pas TLS.
func handshakeCauseOf(err error) string {
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return CauseTimeout
	}
	return CauseNotTLS
}
