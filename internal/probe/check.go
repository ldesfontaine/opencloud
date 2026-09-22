package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

// Ce que la cible lit de nous dans ses journaux.
const userAgent = "openCloud"

// errTooManyRedirects : la cible renvoie plus loin que ce qu'on suit.
var errTooManyRedirects = errors.New("too many redirects")

// Checker exécute les sondes d'une machine. Il porte les autorités
// racines de cette machine-là : celles du système, plus celle que
// l'opérateur y a ajoutée. C'est la machine qui sonde qui juge une
// chaîne, jamais le serveur à sa place.
type Checker struct {
	// roots à nil vaut le magasin du système, que crypto/x509 lit ainsi.
	roots *x509.CertPool
}

func NewChecker(roots *x509.CertPool) Checker {
	return Checker{roots: roots}
}

// Check exécute une sonde et rend son essai. Elle ne touche ni la base ni
// le réseau d'openCloud : l'agent l'appelle chez lui, la machine openCloud
// dans son propre processus.
func (c Checker) Check(ctx context.Context, task Task, now time.Time) Result {
	if task.Kind == KindTCP {
		return c.checkTCP(ctx, task, now)
	}
	return c.checkHTTP(ctx, task, now)
}

// checkTCP ouvre la connexion et la referme. Quand la sonde demande TLS,
// elle fait à la place une poignée de main : c'est ce qui donne son
// certificat à un port chiffré qui ne parle pas HTTP.
func (c Checker) checkTCP(ctx context.Context, task Task, now time.Time) Result {
	result := Result{ProbeID: task.ID, CheckedAt: now}
	address, err := ParseTarget(KindTCP, task.Target)
	if err != nil {
		result.Outcome, result.Reason = OutcomeDown, ReasonAddress
		return result
	}
	timeout := task.timeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	if !task.TLS {
		conn, err := egress.Dial(ctx, "tcp", address.String(), timeout)
		result.DurationMs = time.Since(started).Milliseconds()
		if err != nil {
			result.Outcome, result.Reason = OutcomeDown, reasonFor(err)
			return result
		}
		_ = conn.Close()
		result.Outcome = OutcomeUp
		return result
	}
	state, err := inspectTLS(ctx, address, timeout)
	result.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		result.Outcome, result.Reason = OutcomeDown, reasonFor(err)
		return result
	}
	result.Certificate = c.certificateFrom(state, address.Host, now)
	result.Outcome, result.Reason = judgeHandshake(result.Certificate, now)
	return result
}

func (c Checker) checkHTTP(ctx context.Context, task Task, now time.Time) Result {
	result := Result{ProbeID: task.ID, CheckedAt: now}
	address, err := ParseTarget(KindHTTP, task.Target)
	if err != nil {
		result.Outcome, result.Reason = OutcomeDown, ReasonAddress
		return result
	}
	timeout := task.timeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, task.method(), task.Target, nil)
	if err != nil {
		result.Outcome, result.Reason = OutcomeDown, ReasonAddress
		return result
	}
	request.Header.Set("User-Agent", userAgent)

	client := c.newClient(task, timeout)
	defer client.CloseIdleConnections()
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		return c.degradedOrDown(ctx, result, address, err, timeout, now)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, MaxReadBytes))
	result.DurationMs = time.Since(started).Milliseconds()

	code := response.StatusCode
	result.Code = &code
	if response.TLS != nil {
		result.Certificate = c.certificateFrom(*response.TLS, answeringHost(response, address), now)
	}
	result.Outcome, result.Reason = judge(task, code, body)
	return result
}

// answeringHost est le nom que le certificat reçu doit couvrir : celui de
// l'hôte qui a répondu, pas celui de la cible, qu'une redirection suivie
// a pu quitter.
func answeringHost(response *http.Response, target Address) string {
	if response.Request == nil || response.Request.URL == nil {
		return target.Host
	}
	if host := response.Request.URL.Hostname(); host != "" {
		return host
	}
	return target.Host
}

// judge lit la réponse comme la sonde l'attendait : le code d'abord, puis
// le texte, quand l'opérateur en a demandé un.
func judge(task Task, code int, body []byte) (Outcome, Reason) {
	if !matchStatus(task.ExpectedStatus, code) {
		return OutcomeDown, ReasonStatus
	}
	if task.ExpectedBody != "" && !bytes.Contains(body, []byte(task.ExpectedBody)) {
		return OutcomeDown, ReasonBody
	}
	return OutcomeUp, ReasonNone
}

// judgeHandshake conclut ce qu'une poignée de main seule permet : l'hôte a
// répondu, donc l'essai est un succès ; sa chaîne n'en décide que la
// nuance. Une chaîne refusée n'est pas une panne, c'est une confiance
// qu'on ne peut pas établir.
func judgeHandshake(seen *Certificate, now time.Time) (Outcome, Reason) {
	if seen == nil {
		// L'hôte parle TLS mais ne présente rien : il n'y a rien à croire.
		return OutcomeDown, ReasonTLSUntrusted
	}
	switch {
	case seen.Expired(now):
		return OutcomeDegraded, ReasonTLSExpired
	case !seen.ChainValid:
		return OutcomeDegraded, ReasonTLSUntrusted
	case !seen.HostnameMatch:
		return OutcomeDegraded, ReasonTLSHostname
	}
	return OutcomeUp, ReasonNone
}

// degradedOrDown distingue une chaîne refusée d'un hôte injoignable. Une
// poignée de main nue confirme que l'hôte répond et livre sa chaîne, sans
// que la requête reparte : l'essai est alors dégradé, pas hors ligne, et
// l'uptime n'en est pas entamé.
func (c Checker) degradedOrDown(ctx context.Context, result Result, address Address, err error, timeout time.Duration, now time.Time) Result {
	reason, isTrust := trustReason(err)
	if !isTrust {
		result.Outcome, result.Reason = OutcomeDown, reasonFor(err)
		return result
	}
	state, dialErr := inspectTLS(ctx, address, timeout)
	if dialErr != nil {
		result.Outcome, result.Reason = OutcomeDown, reason
		return result
	}
	result.Outcome, result.Reason = OutcomeDegraded, reason
	result.Certificate = c.certificateFrom(state, address.Host, now)
	return result
}

// newClient compose par le dialer gardé, vérifie contre les autorités de
// cette machine, et ne suit les redirections que si la sonde le demande :
// sans cela une redirection vers une page d'erreur passerait pour un
// succès.
func (c Checker) newClient(task Task, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return egress.Dial(ctx, network, address, timeout)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.roots},
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if !task.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= MaxRedirects {
				return errTooManyRedirects
			}
			return nil
		},
	}
}

// reasonFor traduit une erreur réseau en un mot du catalogue : le front ne
// lit jamais le texte d'une erreur Go.
func reasonFor(err error) Reason {
	if errors.Is(err, egress.ErrForbiddenAddress) {
		return ReasonAddress
	}
	if errors.Is(err, errTooManyRedirects) {
		return ReasonRedirect
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ReasonTimeout
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return ReasonDNS
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ReasonRefused
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ReasonTimeout
	}
	return ReasonUnreachable
}

// trustReason dit si l'échec est le refus de la chaîne présentée, par
// opposition à un hôte qui ne répond pas.
func trustReason(err error) (Reason, bool) {
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return ReasonTLSUntrusted, true
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return ReasonTLSHostname, true
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) {
		if invalid.Reason == x509.Expired {
			return ReasonTLSExpired, true
		}
		return ReasonTLSUntrusted, true
	}
	// Go enveloppe les échecs de vérification dans ce type ; les cas
	// nommés plus haut le traversent, arriver ici est un genre non nommé.
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return ReasonTLSUntrusted, true
	}
	return ReasonNone, false
}

// certificateFrom garde les faits de la chaîne présentée et la juge sur
// place. La chaîne et le nom se vérifient à chaque essai, séparément,
// même quand la requête vient d'aboutir : tenir pour vrai ce qu'on n'a
// pas vérifié, c'est ne jamais voir une chaîne cassée.
func (c Checker) certificateFrom(state tls.ConnectionState, host string, now time.Time) *Certificate {
	if len(state.PeerCertificates) == 0 {
		return nil
	}
	leaf := state.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	return &Certificate{
		Subject:       truncate(leaf.Subject.CommonName, MaxIssuerLength),
		Issuer:        truncate(leaf.Issuer.CommonName, MaxIssuerLength),
		NotBefore:     leaf.NotBefore.UTC(),
		NotAfter:      leaf.NotAfter.UTC(),
		Fingerprint:   hex.EncodeToString(sum[:]),
		ChainValid:    c.verifyChain(leaf, state.PeerCertificates[1:], now),
		HostnameMatch: leaf.VerifyHostname(host) == nil,
		OCSP:          stapledStatus(state.OCSPResponse, state.PeerCertificates, now),
	}
}

// verifyChain remonte la chaîne jusqu'à une autorité que cette machine
// connaît, dates comprises. Le nom se vérifie à part : DNSName reste vide
// ici, parce qu'une chaîne impeccable peut servir le mauvais domaine, et
// que les deux faits se lisent séparément sur la fiche.
func (c Checker) verifyChain(leaf *x509.Certificate, intermediates []*x509.Certificate, now time.Time) bool {
	pool := x509.NewCertPool()
	for _, link := range intermediates {
		pool.AddCert(link)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Intermediates: pool,
		Roots:         c.roots,
		CurrentTime:   now,
	})
	return err == nil
}

// stapledStatus lit l'agrafe OCSP que la cible a remise pendant la
// poignée de main. Aucun répondeur n'est contacté : ce qui n'est pas
// agrafé n'est pas su, et se dit en ne disant rien.
func stapledStatus(stapled []byte, chain []*x509.Certificate, now time.Time) OCSP {
	if len(stapled) == 0 {
		return OCSPNone
	}
	if len(chain) < 2 {
		// Sans l'émetteur dans la chaîne, la signature de la réponse ne se
		// vérifie pas : on ne conclut rien plutôt que de croire sur parole.
		return OCSPUnknown
	}
	response, err := ocsp.ParseResponseForCert(stapled, chain[0], chain[1])
	if err != nil {
		return OCSPUnknown
	}
	// Une agrafe périmée ne prouve plus rien : le répondeur a pu révoquer
	// depuis qu'il l'a produite.
	if !response.NextUpdate.IsZero() && response.NextUpdate.Before(now) {
		return OCSPUnknown
	}
	switch response.Status {
	case ocsp.Good:
		return OCSPGood
	case ocsp.Revoked:
		return OCSPRevoked
	default:
		return OCSPUnknown
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
