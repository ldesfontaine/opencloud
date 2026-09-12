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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/web"
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

// Client parle à un serveur openCloud ; il ne sait rien de la boucle.
type Client struct {
	server  string
	http    *http.Client
	version string
}

func NewClient(server, pin, version string) (*Client, error) {
	server = strings.TrimRight(server, "/")
	if !strings.HasPrefix(server, "http://") && !strings.HasPrefix(server, "https://") {
		return nil, fmt.Errorf("server url must start with http:// or https://")
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
		return fmt.Errorf("certificate fingerprint %x does not match the pin", sum)
	}
	return nil
}

func (c *Client) Enroll(ctx context.Context, identity Identity, token string, info hostinfo.Info) (web.EnrollResponse, error) {
	request := web.EnrollRequest{
		MachineID:    identity.MachineID,
		PublicKey:    base64.StdEncoding.EncodeToString(identity.PublicKey),
		Token:        token,
		Hostname:     info.Hostname,
		OS:           info.OS,
		Arch:         info.Arch,
		AgentVersion: c.version,
	}
	var response web.EnrollResponse
	if err := c.postJSON(ctx, "/agent/enroll", request, &response); err != nil {
		return web.EnrollResponse{}, fmt.Errorf("enroll: %w", err)
	}
	return response, nil
}

func (c *Client) challenge(ctx context.Context, machineID string) ([]byte, error) {
	var response web.ChallengeResponse
	if err := c.postJSON(ctx, "/agent/challenge", web.ChallengeRequest{MachineID: machineID}, &response); err != nil {
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
	request.Header.Set(web.HeaderMachine, identity.MachineID)
	request.Header.Set(web.HeaderNonce, base64.StdEncoding.EncodeToString(nonce))
	request.Header.Set(web.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(web.HeaderSignature, base64.StdEncoding.EncodeToString(identity.sign(payload)))
	request.Header.Set(web.HeaderAgentVersion, c.version)
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

// Follow lit le flux jusqu'à sa fin ; « closed » est une fin voulue par le
// serveur, tout le reste est une coupure.
func (s *Stream) Follow() error {
	for {
		name, data, err := readEvent(s.reader)
		if err != nil {
			return err
		}
		if name == "closed" {
			return fmt.Errorf("stream closed by server: %s", data)
		}
	}
}

func (c *Client) Signal(ctx context.Context, session string) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server+"/agent/signal", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+session)
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
