package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	requestTimeout = 15 * time.Second
	maxErrorBody   = 4 << 10
	// Une réponse d'enrôlement tient en quelques centaines d'octets : au-delà,
	// ce n'est pas openCloud qui parle.
	maxAnswerBody = 64 << 10
)

// Une réponse du serveur qui dit non ; le code est celui de son JSON.
type ServerError struct {
	Status int
	Code   string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server answered %d %s", e.Status, e.Code)
}

// Les codes du serveur que l'agent traite à part.
const (
	codeMachineNotFound = "machine_not_found"
	codeBadProof        = "bad_proof"
	codeNoSession       = "no_session"
	codeClockSkew       = "clock_skew"
)

// Permanent dit si réessayer ne servira à rien : la machine n'existe plus,
// sa clé n'est plus celle enrôlée, l'agent est révoqué, l'enrôlement est
// refusé, ou le serveur ne parle pas ce protocole. Un autre 401 ou 404
// n'arrête rien : une horloge décalée se règle, une session perdue se
// rouvre, une preuve déjà vue se refait avec un aléa neuf, un proxy en plein
// redéploiement revient. Seule une signature fausse dit que la clé n'est
// plus la bonne.
func (e *ServerError) Permanent() bool {
	switch e.Status {
	case http.StatusNotFound:
		return e.Code == codeMachineNotFound
	case http.StatusUnauthorized:
		return e.Code == codeBadProof || e.Code == agentwire.CodeRevoked
	case http.StatusConflict, http.StatusUpgradeRequired:
		return true
	}
	return false
}

var (
	// ErrPlainRefused : le serveur est en http:// et n'est pas sur cette
	// machine ; le jeton et les signaux passeraient en clair sur le réseau.
	ErrPlainRefused = errors.New("plain http to a remote server refused")
	// ErrPinMismatch : le certificat présenté n'est pas celui épinglé ; il a
	// changé, ou quelqu'un se fait passer pour openCloud.
	ErrPinMismatch = errors.New("certificate does not match the pin")
)

// Client parle à un serveur openCloud ; il ne sait rien de la boucle.
type Client struct {
	server  string
	http    *http.Client
	version string
	// Le système de la machine, redit à chaque ouverture du flux.
	system string
	// Les droits du fichier de l'agent, redits à chaque ouverture du flux.
	rights string
	// La racine des services du fichier de l'agent, redite de même.
	root string
	// Le compte sous lequel l'agent tourne, et les droits cochés qu'il ne peut
	// pas exercer : jugés avant chaque ouverture du flux, redits avec lui.
	account     string
	unavailable string
	// Le retour arrière que l'agent sait faire (0 : aucun).
	rollback int
}

// SetAccount fixe le compte que l'agent dit à l'ouverture du flux.
func (c *Client) SetAccount(account machine.Account) {
	c.account = machine.FormatAccount(account)
}

// SetUnavailable fixe les droits cochés que le compte de l'agent ne peut pas
// exercer, avec leur raison. Une table vide efface le rapport précédent.
func (c *Client) SetUnavailable(unavailable map[string]string) {
	c.unavailable = machine.FormatUnavailable(unavailable)
}

// SetRollbackVersion fixe la version du retour arrière que l'agent rapporte à
// l'ouverture du flux ; 0 : rien n'est dit, le serveur refuse alors de le
// mettre à jour.
func (c *Client) SetRollbackVersion(version int) {
	c.rollback = version
}

// SetRoot fixe la racine des services que l'agent rapporte à l'ouverture du
// flux : celle de son fichier.
func (c *Client) SetRoot(root string) {
	c.root = root
}

// SetCapabilities fixe les droits que l'agent rapporte à l'ouverture du
// flux : ceux de son fichier.
func (c *Client) SetCapabilities(capabilities []machine.Capability) {
	c.rights = machine.FormatCapabilities(capabilities)
}

// NewClient refuse http:// vers autre chose que la boucle locale, sauf si
// allowPlain le dit : un tunnel WireGuard ou un LAN de confiance, en
// connaissance de cause.
func NewClient(server, pin, version string, allowPlain bool) (*Client, error) {
	server = strings.TrimRight(server, "/")
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		return nil, fmt.Errorf("server url must start with http:// or https://")
	}
	if strings.HasPrefix(server, "http://") && !allowPlain && !isLoopback(server) {
		return nil, ErrPlainRefused
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if pin != "" {
		tlsConfig, err := pinnedTLS(pin)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}
	// Une redirection n'est pas suivie : elle se lit comme une réponse, donc
	// un refus. Le serveur n'en fait jamais vers l'agent ; un mandataire qui
	// en fait enverrait le jeton ou la preuve ailleurs.
	httpClient := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{server: server, http: httpClient, version: version, system: hostinfo.OSName()}, nil
}

// newRequest prépare une requête vers le serveur, avec la version du
// protocole que l'agent parle.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.server+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set(agentwire.HeaderProtocol, agentwire.ProtocolVersion)
	return request, nil
}

func isLoopback(server string) bool {
	parsed, err := url.Parse(server)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pinnedTLS accepte un seul certificat, celui dont l'empreinte SHA-256 est
// donnée : le cas d'un openCloud sans domaine, au certificat auto-signé.
func pinnedTLS(pin string) (*tls.Config, error) {
	expected, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(pin), "sha256:"))
	if err != nil || len(expected) != sha256.Size {
		return nil, fmt.Errorf("pin must be the sha256 fingerprint of the certificate, in hex")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // #nosec G402 -- la chaîne n'est pas vérifiée, l'empreinte l'est, juste en dessous.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("no certificate presented")
			}
			return verifyPin(state.PeerCertificates[0], expected)
		},
	}, nil
}

func verifyPin(certificate *x509.Certificate, expected []byte) error {
	sum := sha256.Sum256(certificate.Raw)
	if !bytes.Equal(sum[:], expected) {
		return fmt.Errorf("%w: fingerprint %x", ErrPinMismatch, sum)
	}
	return nil
}

func (c *Client) Enroll(ctx context.Context, identity Identity, token string, info hostinfo.Info) (agentwire.EnrollResponse, error) {
	request := agentwire.EnrollRequest{
		MachineID:    identity.MachineID,
		PublicKey:    base64.StdEncoding.EncodeToString(identity.PublicKey),
		Token:        token,
		Hostname:     info.Hostname,
		OS:           info.OS,
		Arch:         info.Arch,
		AgentVersion: c.version,

		HostFingerprint: info.HostFingerprint,
	}
	var response agentwire.EnrollResponse
	if err := c.postJSON(ctx, "/agent/enroll", request, &response); err != nil {
		return agentwire.EnrollResponse{}, fmt.Errorf("enroll: %w", err)
	}
	return response, nil
}

// Stream est le flux ouvert : le jeton de session et ce que le serveur dit.
type Stream struct {
	Session string
	body    io.ReadCloser
	reader  *bufio.Reader
}

func (s *Stream) Close() error {
	return s.body.Close()
}

// OpenStream signe une preuve neuve (protocole 2 : un aléa tiré ici, la
// machine, l'heure), ouvre le flux et attend le jeton de session.
func (c *Client) OpenStream(ctx context.Context, identity Identity) (*Stream, error) {
	nonce := machine.NewNonce()
	timestamp := time.Now().Unix()
	payload, err := machine.SignedPayload(nonce, identity.MachineID, timestamp)
	if err != nil {
		return nil, fmt.Errorf("build proof: %w", err)
	}
	request, err := c.newRequest(ctx, http.MethodGet, "/agent/stream", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(agentwire.HeaderMachine, identity.MachineID)
	request.Header.Set(agentwire.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	request.Header.Set(agentwire.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(agentwire.HeaderSignature, base64.StdEncoding.EncodeToString(identity.sign(payload)))
	request.Header.Set(agentwire.HeaderAgentVersion, c.version)
	// Un système au nom hors ASCII ne passe pas dans un en-tête : il reste
	// celui de l'enrôlement.
	if machine.IsSystemName(c.system) {
		request.Header.Set(agentwire.HeaderAgentOS, c.system)
	}
	if c.rights != "" {
		request.Header.Set(agentwire.HeaderAgentRights, c.rights)
	}
	if fingerprint := identity.ServerKeyFingerprint(); fingerprint != "" {
		request.Header.Set(agentwire.HeaderServerKey, fingerprint)
	}
	if c.root != "" {
		request.Header.Set(agentwire.HeaderAgentRoot, c.root)
	}
	if c.account != "" {
		request.Header.Set(agentwire.HeaderAgentAccount, c.account)
	}
	if c.unavailable != "" {
		request.Header.Set(agentwire.HeaderAgentUnavailable, c.unavailable)
	}
	if c.rollback > 0 {
		request.Header.Set(agentwire.HeaderAgentRollback, strconv.Itoa(c.rollback))
	}
	request.Header.Set("Accept", "text/event-stream")

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, serverError(response)
	}
	reader := bufio.NewReaderSize(response.Body, agentwire.MaxEventLineBytes)
	name, data, err := readEvent(reader)
	if err != nil || name != "session" || data == "" {
		_ = response.Body.Close()
		return nil, fmt.Errorf("stream did not open with a session: %v", err)
	}
	return &Stream{Session: data, body: response.Body, reader: reader}, nil
}

// Follow lit le flux jusqu'à sa fin et passe chaque commande du serveur
// à handle ; « closed » est une fin voulue par le serveur, tout le reste
// est une coupure.
func (s *Stream) Follow(handle func(name, data string)) error {
	for {
		name, data, err := readEvent(s.reader)
		if err != nil {
			return err
		}
		switch name {
		case "closed":
			return fmt.Errorf("stream closed by server: %s", data)
		case "ping", "session", "":
		default:
			handle(name, data)
		}
	}
}

// Signal donne signe de vie et livre ce qui attendait : les lectures de la
// machine, ce que Docker a montré, ce que les sondes ont donné, ce qu'il a
// fait des ordres ; sans rien, le signal part sans corps.
func (c *Client) Signal(ctx context.Context, session string, sent agentwire.SignalRequest) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var body io.Reader
	if !isEmptySignal(sent) {
		content, err := json.Marshal(sent)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(content)
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/agent/signal", body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set(agentwire.HeaderSignalSections, "1")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("signal: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var answer agentwire.SignalResponse
		if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody)).Decode(&answer); err != nil {
			return nil, fmt.Errorf("decode signal answer: %w", err)
		}
		return answer.Rejected, nil
	default:
		return nil, fmt.Errorf("signal: %w", serverError(response))
	}
}

func isEmptySignal(sent agentwire.SignalRequest) bool {
	return len(sent.Readings) == 0 && sent.Services == nil && sent.Probes == nil && sent.Images == nil && sent.Actions == nil && sent.SSHKeys == nil && sent.Roles == nil
}

// PostLogs livre un lot de journal pour une requête du serveur.
func (c *Client) PostLogs(ctx context.Context, session, requestID string, batch container.LogBatch) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	content, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/agent/logs/"+url.PathEscape(requestID), bytes.NewReader(content))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("post logs: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("post logs: %w", serverError(response))
	}
	return nil
}

// GetParcel ouvre un fichier d'un colis que le serveur a préparé pour cette
// machine, sous le jeton de session du flux. Pas de délai propre : un fichier
// se compte en dizaines de Mio, le contexte de l'appelant borne la durée. Le
// corps est à fermer ; la taille est celle que le serveur annonce, l'ordre
// signé fait foi.
func (c *Client) GetParcel(ctx context.Context, session, parcel, name string) (io.ReadCloser, int64, error) {
	request, err := c.newRequest(ctx, http.MethodGet, agentwire.ParcelPath(parcel, name), nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("get parcel: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, 0, fmt.Errorf("get parcel: %w", serverError(response))
	}
	return response.Body, response.ContentLength, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload, into any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := c.newRequest(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	// Une redirection, non suivie, est un refus comme un autre.
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return serverError(response)
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxAnswerBody)).Decode(into)
}

func serverError(response *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	content, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	_ = json.Unmarshal(content, &body)
	return &ServerError{Status: response.StatusCode, Code: body.Error}
}

// PutParcel téléverse un fichier dans le colis que le serveur a ouvert à cette
// machine, sous le jeton de session du flux : le corps est le fichier, la taille
// et la somme sont annoncées en en-têtes. Le serveur répond 201. « Expect »
// laisse le serveur refuser (colis inconnu, disque plein) avant qu'un seul
// octet ne parte. Pas de délai propre, comme GetParcel : le contexte de
// l'appelant borne la durée.
func (c *Client) PutParcel(ctx context.Context, session, parcel, name string, body io.Reader, size int64, sum string) error {
	request, err := c.newRequest(ctx, http.MethodPut, agentwire.ParcelPath(parcel, name), io.NopCloser(body))
	if err != nil {
		return err
	}
	request.ContentLength = size
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(agentwire.HeaderParcelSHA256, sum)
	request.Header.Set("Expect", "100-continue")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("put parcel: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("put parcel: %w", serverError(response))
	}
	return nil
}
