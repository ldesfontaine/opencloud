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

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/service"
)

const (
	requestTimeout = 15 * time.Second
	maxErrorBody   = 4 << 10
)

// Une réponse du serveur qui dit non ; le code est celui de son JSON.
type ServerError struct {
	Status int
	Code   string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server answered %d %s", e.Status, e.Code)
}

// Permanent dit si réessayer ne servira à rien : l'identité est refusée ou
// la machine n'existe plus.
func (e *ServerError) Permanent() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusNotFound || e.Status == http.StatusConflict
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
	return &Client{server: server, http: &http.Client{Transport: transport}, version: version}, nil
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

func (c *Client) Enroll(ctx context.Context, identity Identity, token string, info hostinfo.Info) (machine.EnrollResponse, error) {
	request := machine.EnrollRequest{
		MachineID:    identity.MachineID,
		PublicKey:    base64.StdEncoding.EncodeToString(identity.PublicKey),
		Token:        token,
		Hostname:     info.Hostname,
		OS:           info.OS,
		Arch:         info.Arch,
		AgentVersion: c.version,
	}
	var response machine.EnrollResponse
	if err := c.postJSON(ctx, "/agent/enroll", request, &response); err != nil {
		return machine.EnrollResponse{}, fmt.Errorf("enroll: %w", err)
	}
	return response, nil
}

func (c *Client) challenge(ctx context.Context, machineID string) ([]byte, error) {
	var response machine.ChallengeResponse
	if err := c.postJSON(ctx, "/agent/challenge", machine.ChallengeRequest{MachineID: machineID}, &response); err != nil {
		return nil, fmt.Errorf("challenge: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(response.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	return nonce, nil
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

// OpenStream relève le défi, ouvre le flux et attend le jeton de session.
func (c *Client) OpenStream(ctx context.Context, identity Identity) (*Stream, error) {
	nonce, err := c.challenge(ctx, identity.MachineID)
	if err != nil {
		return nil, err
	}
	timestamp := time.Now().Unix()
	payload, err := machine.SignedPayload(nonce, identity.MachineID, timestamp)
	if err != nil {
		return nil, fmt.Errorf("build proof: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server+"/agent/stream", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set(machine.HeaderMachine, identity.MachineID)
	request.Header.Set(machine.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	request.Header.Set(machine.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(machine.HeaderSignature, base64.StdEncoding.EncodeToString(identity.sign(payload)))
	request.Header.Set(machine.HeaderAgentVersion, c.version)
	request.Header.Set("Accept", "text/event-stream")

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, serverError(response)
	}
	reader := bufio.NewReader(response.Body)
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
// machine, ce que Docker a montré et ce que les sondes ont donné ; sans
// rien, le signal part sans corps.
func (c *Client) Signal(ctx context.Context, session string, readings []sampler.Reading, report *service.Report, checked *probe.Report) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var body io.Reader
	if len(readings) > 0 || report != nil || checked != nil {
		content, err := json.Marshal(machine.SignalRequest{Readings: readings, Services: report, Probes: checked})
		if err != nil {
			return err
		}
		body = bytes.NewReader(content)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/agent/signal", body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("signal: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("signal: %w", serverError(response))
	}
	return nil
}

// PostLogs livre un lot de journal pour une requête du serveur.
func (c *Client) PostLogs(ctx context.Context, session, requestID string, batch service.LogBatch) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	content, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/agent/logs/"+url.PathEscape(requestID), bytes.NewReader(content))
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

func (c *Client) postJSON(ctx context.Context, path string, payload, into any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return serverError(response)
	}
	return json.NewDecoder(response.Body).Decode(into)
}

func serverError(response *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	content, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	_ = json.Unmarshal(content, &body)
	return &ServerError{Status: response.StatusCode, Code: body.Error}
}
