package restic

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// La patience du mandataire (limite n° 1) : restic réessaie un 5xx plus de
// quinze minutes. Après DefaultPatience de 5xx continus, le mandataire lui
// répond 403 et la sauvegarde échoue proprement ; l'orchestrateur la rejoue.
const DefaultPatience = 15 * time.Second

const (
	proxyTokenBytes        = 16
	proxyReadHeaderTimeout = 10 * time.Second
	// Le code que restic reçoit quand la patience est épuisée.
	patienceExhausted = "relay_patience_exhausted"
	tokenRefused      = "token_refused"
)

// Les en-têtes de la requête de restic que le relais transporte.
var relayedRequestHeaders = []string{"Accept", "Range"}

// ProxyConfig : où relayer (l'adresse du relais du serveur pour une cible,
// …/agent/relay/rest/<cible>), avec quel client (celui de l'agent, qui fait
// confiance au serveur), et comment signer au nom de la machine.
type ProxyConfig struct {
	Endpoint  *url.URL
	Client    *http.Client
	MachineID string
	Sign      func(payload []byte) []byte
	Patience  time.Duration
	Now       func() time.Time
}

// Proxy est le mandataire local de la source, le temps d'une sauvegarde par
// le relais : il écoute sur la boucle locale, signe chaque requête de restic
// et la passe au relais. Un jeton tiré au démarrage, premier segment de
// l'URL, écarte les autres processus de la machine (limite n° 10).
type Proxy struct {
	config   ProxyConfig
	token    string
	listener net.Listener
	server   *http.Server

	mu           sync.Mutex
	failingSince time.Time
}

func NewProxy(config ProxyConfig) *Proxy {
	if config.Patience <= 0 {
		config.Patience = DefaultPatience
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Proxy{config: config}
}

// Start écoute sur un port éphémère de 127.0.0.1 et rend l'URL du dépôt à
// donner à restic (RESTIC_REPOSITORY du seul processus fils).
func (p *Proxy) Start() (string, error) {
	raw := make([]byte, proxyTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("draw proxy token: %w", err)
	}
	p.token = hex.EncodeToString(raw)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen proxy: %w", err)
	}
	p.listener = listener
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: proxyReadHeaderTimeout}
	go func() { _ = p.server.Serve(listener) }()
	port := listener.Addr().(*net.TCPAddr).Port
	return "rest:http://127.0.0.1:" + strconv.Itoa(port) + "/" + p.token + "/", nil
}

func (p *Proxy) Close() error {
	if p.server == nil {
		return nil
	}
	return p.server.Close()
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoPath, ok := p.stripToken(r.URL.EscapedPath())
	if !ok {
		writeRelayError(w, http.StatusForbidden, tokenRefused)
		return
	}
	// Premier étage : ce qui ne passerait pas au relais ne part pas.
	if err := Allow(r.Method, repoPath, r.URL.RawQuery); err != nil {
		status, code := RelayStatus(err)
		writeRelayError(w, status, code)
		return
	}
	response, err := p.forward(r, repoPath)
	if err != nil {
		p.answerFailure(w, http.StatusBadGateway, "relay_unreachable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusInternalServerError {
		p.answerFailure(w, response.StatusCode, "relay_failed")
		return
	}
	p.noteAnswer(response.StatusCode)
	copyResponse(w, response)
}

// stripToken retire le jeton du mandataire, comparé en temps constant.
func (p *Proxy) stripToken(escapedPath string) (string, bool) {
	prefix := "/" + p.token
	if len(escapedPath) <= len(prefix) || subtle.ConstantTimeCompare([]byte(escapedPath[:len(prefix)]), []byte(prefix)) != 1 {
		return "", false
	}
	rest := escapedPath[len(prefix):]
	if !strings.HasPrefix(rest, "/") {
		return "", false
	}
	return rest, true
}

// forward signe et passe la requête au relais. La preuve couvre (aléa,
// machine, heure), pas la requête : c'est TLS vers le serveur qui la protège.
func (p *Proxy) forward(r *http.Request, repoPath string) (*http.Response, error) {
	target := *p.config.Endpoint
	target.Path = strings.TrimSuffix(target.Path, "/") + repoPath
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery
	body := r.Body
	if r.ContentLength == 0 {
		body = http.NoBody
	}
	outbound, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build relay request: %w", err)
	}
	outbound.ContentLength = r.ContentLength
	for _, name := range relayedRequestHeaders {
		if value := r.Header.Get(name); value != "" {
			outbound.Header.Set(name, value)
		}
	}
	if err := p.sign(outbound.Header); err != nil {
		return nil, err
	}
	// L'hôte est celui du serveur, fixé à la configuration ; seuls le chemin et la
	// requête viennent de restic, déjà jugés par Allow.
	return p.config.Client.Do(outbound) // #nosec G704 -- hôte fixe : le serveur d'openCloud.
}

func (p *Proxy) sign(header http.Header) error {
	nonce := machine.NewNonce()
	timestamp := p.config.Now().Unix()
	payload, err := machine.SignedPayload(nonce, p.config.MachineID, timestamp)
	if err != nil {
		return fmt.Errorf("build proof: %w", err)
	}
	header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	header.Set(agentwire.HeaderMachine, p.config.MachineID)
	header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(p.config.Sign(payload)))
	return nil
}

// answerFailure rend le 5xx à restic, qui réessaiera, tant que la patience
// tient ; au-delà, un 403 qui l'arrête.
func (p *Proxy) answerFailure(w http.ResponseWriter, status int, code string) {
	if p.isOutOfPatience() {
		writeRelayError(w, http.StatusForbidden, patienceExhausted)
		return
	}
	writeRelayError(w, status, code)
}

// isOutOfPatience note un échec et dit si les échecs durent depuis plus que
// la patience.
func (p *Proxy) isOutOfPatience() bool {
	now := p.config.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failingSince.IsZero() {
		p.failingSince = now
	}
	return now.Sub(p.failingSince) >= p.config.Patience
}

// noteAnswer : seul un 2xx remet le compteur à zéro. restic intercale un
// DELETE de nettoyage refusé 403 entre ses essais : le compter comme une
// réponse saine rendrait la patience sans fin.
func (p *Proxy) noteAnswer(status int) {
	if status < 200 || status > 299 {
		return
	}
	p.mu.Lock()
	p.failingSince = time.Time{}
	p.mu.Unlock()
}

func copyResponse(w http.ResponseWriter, response *http.Response) {
	for _, name := range agentwire.ResticRelayResponseHeaders() {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

// writeRelayError répond comme l'essai : {"error":"<code>"}, code fermé.
func writeRelayError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"`+code+`"}`)
}
