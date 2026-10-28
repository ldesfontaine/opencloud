package opnsense

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/egress"
	"github.com/ldesfontaine/opencloud/internal/firewall"
)

const (
	requestTimeout = 20 * time.Second
	// Un pare-feu chargé de milliers de règles tient dans bien moins ; au-delà,
	// ce n'est plus l'API qu'on croit lire.
	maxResponseBytes = 8 << 20

	// Chaque texte que l'API rend est borné avant d'entrer en base et à
	// l'écran : un texte plus long ne dit rien de plus.
	maxTextBytes = 256
)

// errPinMismatch : le certificat présenté n'est pas celui dont l'opérateur a
// donné l'empreinte.
var errPinMismatch = errors.New("certificate does not match the pin")

// Client parle à l'API d'un OPNsense, en lecture. Il ne garde aucune connexion
// ouverte.
type Client struct {
	http    *http.Client
	baseURL string
	apiKey  string
	secret  string
}

// New est la fabrique que le câblage donne au service. La paire de clés part
// dans l'en-tête Authorization et nulle part ailleurs : ni dans l'adresse, ni
// dans une erreur.
func New(credentials firewall.Credentials) firewall.Connector {
	transport := &http.Transport{
		// Pas de mandataire d'environnement : tout sort par la garde egress.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Le réseau local est le cas normal d'un pare-feu ; le lien-local
			// (métadonnées des hébergeurs) reste fermé.
			return egress.DialWith(ctx, network, address, requestTimeout, true)
		},
		TLSClientConfig:       tlsConfig(credentials.Pin),
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
		DisableKeepAlives:     true,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			// Une API qui redirige n'est pas celle qu'on a déclarée, et suivre la
			// redirection renverrait la paire de clés ailleurs.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL: credentials.APIURL,
		apiKey:  credentials.APIKey,
		secret:  credentials.APISecret,
	}
}

// tlsConfig juge le certificat par le magasin du système, ou, avec une
// empreinte, par elle seule : un OPNsense livre un certificat auto-signé.
func tlsConfig(pin string) *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if pin == "" {
		return config
	}
	expected, err := hex.DecodeString(pin)
	if err != nil {
		// Definition.Normalize a déjà refusé ce qui n'est pas de l'hexadécimal.
		expected = nil
	}
	config.InsecureSkipVerify = true // #nosec G402 -- la chaîne n'est pas vérifiée, l'empreinte l'est, juste en dessous.
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errPinMismatch
		}
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		if !bytes.Equal(sum[:], expected) {
			return errPinMismatch
		}
		return nil
	}
	return config
}

// httpStatusError porte le code d'une réponse que l'appelant veut distinguer
// (un point d'accès absent d'une installation), sans être un refus.
type httpStatusError struct{ status int }

func (e httpStatusError) Error() string { return fmt.Sprintf("status %d", e.status) }

// get lit une ressource JSON. C'est le seul endroit du paquet qui émet une
// requête, et il n'émet que des GET. Chaque échec devient l'un des quatre
// refus du paquet firewall, avec sa cause dans la chaîne pour le journal ; un
// code 404 ou 403 reste reconnaissable par isStatus pour les sources
// facultatives.
func (c *Client) get(ctx context.Context, path string, query url.Values, into any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", firewall.ErrBadResponse, err)
	}
	request.SetBasicAuth(c.apiKey, c.secret)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return transportRefusal(err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %w", firewall.ErrUnauthorized, httpStatusError{response.StatusCode})
	case response.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: %w", firewall.ErrBadResponse, httpStatusError{response.StatusCode})
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return transportRefusal(err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("%w: answer over %d bytes", firewall.ErrBadResponse, maxResponseBytes)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: decode answer: %w", firewall.ErrBadResponse, err)
	}
	return nil
}

// isStatus dit si l'échec est une réponse de ce code.
func isStatus(err error, status int) bool {
	var coded httpStatusError
	return errors.As(err, &coded) && coded.status == status
}

// transportRefusal range un échec de transport : un certificat refusé se
// distingue d'un hôte qui ne répond pas, c'est ce qui dit d'épingler.
func transportRefusal(err error) error {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.Is(err, errPinMismatch) || errors.As(err, &verification) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return fmt.Errorf("%w: %w", firewall.ErrCertificate, err)
	}
	return fmt.Errorf("%w: %w", firewall.ErrUnreachable, err)
}

// boundedText coupe un texte de l'API à maxTextBytes, sans couper un caractère
// en deux.
func boundedText(text string) string {
	if len(text) <= maxTextBytes {
		return text
	}
	cut := maxTextBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
