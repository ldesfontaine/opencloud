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
)

// Ce que la cible lit de nous dans ses journaux.
const userAgent = "openCloud"

// errTooManyRedirects : la cible renvoie plus loin que ce qu'on suit.
var errTooManyRedirects = errors.New("too many redirects")

// Check exécute une sonde et rend son essai. Elle ne touche ni la base ni
// le réseau d'openCloud : l'agent l'appelle chez lui, la machine openCloud
// dans son propre processus.
func Check(ctx context.Context, task Task, now time.Time) Result {
	if task.Kind == KindTCP {
		return checkTCP(ctx, task, now)
	}
	return checkHTTP(ctx, task, now)
}

func checkTCP(ctx context.Context, task Task, now time.Time) Result {
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
	conn, err := guardedDial(ctx, "tcp", address.String(), timeout)
	result.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		result.Outcome, result.Reason = OutcomeDown, reasonFor(err)
		return result
	}
	_ = conn.Close()
	result.Outcome = OutcomeUp
	return result
}

func checkHTTP(ctx context.Context, task Task, now time.Time) Result {
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

	client := newClient(task, timeout)
	defer client.CloseIdleConnections()
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		result.DurationMs = time.Since(started).Milliseconds()
		return degradedOrDown(ctx, result, address, err, timeout)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, MaxReadBytes))
	result.DurationMs = time.Since(started).Milliseconds()

	code := response.StatusCode
	result.Code = &code
	if response.TLS != nil {
		result.Certificate = certificateFrom(response.TLS.PeerCertificates)
	}
	result.Outcome, result.Reason = judge(task, code, body)
	return result
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

// degradedOrDown distingue une chaîne refusée d'un hôte injoignable. Une
// poignée de main nue confirme que l'hôte répond et livre sa chaîne, sans
// que la requête reparte : l'essai est alors dégradé, pas hors ligne, et
// l'uptime n'en est pas entamé.
func degradedOrDown(ctx context.Context, result Result, address Address, err error, timeout time.Duration) Result {
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
	result.Certificate = certificateFrom(state.PeerCertificates)
	return result
}

// newClient compose par le dialer gardé, et ne suit les redirections que si
// la sonde le demande : sans cela une redirection vers une page d'erreur
// passerait pour un succès.
func newClient(task Task, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return guardedDial(ctx, network, address, timeout)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
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
	if errors.Is(err, errForbiddenAddress) {
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

// certificateFrom garde les faits du certificat de la cible, jamais la
// chaîne entière : la fonctionnalité certificats les lira.
func certificateFrom(chain []*x509.Certificate) *Certificate {
	if len(chain) == 0 {
		return nil
	}
	leaf := chain[0]
	sum := sha256.Sum256(leaf.Raw)
	return &Certificate{
		Subject:     truncate(leaf.Subject.CommonName, MaxIssuerLength),
		Issuer:      truncate(leaf.Issuer.CommonName, MaxIssuerLength),
		NotBefore:   leaf.NotBefore.UTC(),
		NotAfter:    leaf.NotAfter.UTC(),
		Fingerprint: hex.EncodeToString(sum[:]),
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
