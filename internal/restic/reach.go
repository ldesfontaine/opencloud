package restic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Les délais de la sonde d'avant chaque sauvegarde : courts, car une cible
// qui ne répond pas vite part sur le relais au lieu de faire attendre.
const (
	probeDialTimeout      = 3 * time.Second
	probeHandshakeTimeout = 5 * time.Second
)

// Dialer compose la connexion TCP de la sonde ; l'agent passe sa garde de
// sortie, les tests une boucle locale ou un faux.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// Target est ce que la source sait d'une cible après restic.provision.
// Zéro tant qu'elle n'est pas provisionnée.
type Target struct {
	Host           string
	Port           int
	CertificatePEM string
}

func (t Target) IsProvisioned() bool {
	return t.Host != "" && t.Port != 0 && t.CertificatePEM != ""
}

func (t Target) address() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// Hint dit ce qu'il faudrait ouvrir pour passer en direct : la source doit
// joindre TargetHost:TargetPort. La phrase est rendue par l'interface.
type Hint struct {
	TargetHost string
	TargetPort int
}

// Choice est le chemin retenu pour une sauvegarde, et pourquoi.
type Choice struct {
	Path   string
	Reason string
	Hint   Hint
}

// Report rend le chemin pour le résultat de l'exécution.
func (c Choice) Report() agentwire.ResticRunReport {
	return agentwire.ResticRunReport{
		Path:       c.Path,
		Reason:     c.Reason,
		TargetHost: c.Hint.TargetHost,
		TargetPort: c.Hint.TargetPort,
	}
}

var errCertificateUnusable = errors.New("restic: pinned certificate unusable")

// Probe dit si la cible répond en TLS avec le certificat épinglé : une
// raison de agentwire.ResticPathReasons. TCP d'abord, pour distinguer une
// cible injoignable (pare-feu, éteinte) d'une cible qui répond avec un autre
// certificat.
func Probe(ctx context.Context, dialer Dialer, target Target) string {
	if !target.IsProvisioned() {
		return agentwire.ResticTargetNotProvisioned
	}
	dialContext, cancelDial := context.WithTimeout(ctx, probeDialTimeout)
	defer cancelDial()
	conn, err := dialer.DialContext(dialContext, "tcp", target.address())
	if err != nil {
		return agentwire.ResticTCPUnreachable
	}
	defer func() { _ = conn.Close() }()
	if err := handshakePinned(ctx, conn, target); err != nil {
		return agentwire.ResticTLSFailed
	}
	return agentwire.ResticReachable
}

// handshakePinned mène la poignée de main avec le certificat de la cible pour
// seule racine : ni le magasin du système, ni un autre certificat signé par
// une autorité reconnue ne passent. Le nom vérifié est l'hôte annoncé, celui
// que restic utilisera.
func handshakePinned(ctx context.Context, conn net.Conn, target Target) error {
	config, err := pinnedTLSConfig(target)
	if err != nil {
		return err
	}
	handshakeContext, cancel := context.WithTimeout(ctx, probeHandshakeTimeout)
	defer cancel()
	client := tls.Client(conn, config)
	return client.HandshakeContext(handshakeContext)
}

// pinnedTLSConfig rend la configuration TLS qui épingle le certificat de la
// cible : ce que fait RESTIC_CACERT pour restic, ici pour la sonde.
func pinnedTLSConfig(target Target) (*tls.Config, error) {
	pinned, err := agentwire.ParsePublicCertificate(target.CertificatePEM)
	if err != nil {
		return nil, errCertificateUnusable
	}
	roots := x509.NewCertPool()
	roots.AddCert(pinned)
	return &tls.Config{
		RootCAs:    roots,
		ServerName: target.Host,
		// rest-server est lancé en --tls-min-ver 1.3.
		MinVersion: tls.VersionTLS13,
	}, nil
}

// Choose sonde la cible et rend le chemin. Le relais sert seulement à la
// source qui ne joint pas la cible ; forceRelay vient de l'ordre.
func Choose(ctx context.Context, dialer Dialer, target Target, forceRelay bool) Choice {
	hint := Hint{TargetHost: target.Host, TargetPort: target.Port}
	if forceRelay {
		return Choice{Path: agentwire.ResticPathRelay, Reason: agentwire.ResticRelayForced, Hint: hint}
	}
	reason := Probe(ctx, dialer, target)
	if reason == agentwire.ResticReachable {
		return Choice{Path: agentwire.ResticPathDirect, Reason: reason}
	}
	return Choice{Path: agentwire.ResticPathRelay, Reason: reason, Hint: hint}
}
