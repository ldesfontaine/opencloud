package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

const (
	proxmoxAPIPrefix      = "/api2/json"
	proxmoxRequestTimeout = 20 * time.Second
	// Une réponse de l'API locale sur un invité ou une tâche tient dans bien
	// moins ; la liste de toute la grappe, dans quelques centaines de Kio.
	maxProxmoxResponseBytes = 4 << 20
	maxProxmoxMessageBytes  = 200
)

// upidPattern est la forme d'un identifiant de tâche de Proxmox. Il entre dans
// un chemin d'URL : une réponse qui n'a pas cette forme n'y entre pas.
var upidPattern = regexp.MustCompile(`^UPID:[A-Za-z0-9.-]+:[0-9A-Fa-f]{8}:[0-9A-Fa-f]{8,}:[0-9A-Fa-f]{8}:[a-z_]+:[A-Za-z0-9._-]*:[A-Za-z0-9._@!-]+:$`)

// nodeNamePattern : un nom de nœud de Proxmox est un nom d'hôte. Il vient de
// l'API et entre dans un chemin.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})$`)

// proxmoxAPI parle à l'API de Proxmox sur la boucle locale avec le jeton de
// l'agent. Le certificat se juge par l'empreinte du fichier, seule. Le jeton
// part dans l'en-tête Authorization et nulle part ailleurs : ni dans une URL,
// ni dans une erreur, ni dans un journal.
type proxmoxAPI struct {
	http          *http.Client
	baseURL       string
	authorization string
}

// newProxmoxAPI prépare le client. Sans empreinte valide, il refuse : aucune
// connexion non vérifiée.
func newProxmoxAPI(baseURL string, token proxmoxToken) (*proxmoxAPI, error) {
	tlsConfig, err := proxmox.PinnedTLSConfig(token.Fingerprint)
	if err != nil {
		return nil, &pveError{reason: agentwire.ReasonProxmoxPinMissing, message: "no usable certificate fingerprint"}
	}
	return &proxmoxAPI{
		http: &http.Client{
			Transport: proxmoxTransport(tlsConfig),
			Timeout:   proxmoxRequestTimeout,
			// Une API locale qui redirige n'est pas celle qu'on a épinglée.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL:       strings.TrimSuffix(baseURL, "/") + proxmoxAPIPrefix,
		authorization: "PVEAPIToken=" + token.ID + "=" + token.Secret,
	}, nil
}

func proxmoxTransport(tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		// Pas de mandataire d'environnement : l'API est sur la machine.
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: proxmoxRequestTimeout}).DialContext,
		TLSClientConfig:       tlsConfig,
		TLSHandshakeTimeout:   proxmoxRequestTimeout,
		ResponseHeaderTimeout: proxmoxRequestTimeout,
		DisableKeepAlives:     true,
	}
}

// pveError est un échec de l'API avec la raison de refus qui le dit. Son texte
// ne porte jamais le jeton.
type pveError struct {
	reason  string
	message string
}

func (e *pveError) Error() string {
	return e.reason + ": " + e.message
}

// pveGuest est une ligne de /cluster/resources?type=vm.
type pveGuest struct {
	Type     string `json:"type"`
	VMID     int    `json:"vmid"`
	Node     string `json:"node"`
	Status   string `json:"status"`
	Template int    `json:"template"`
}

// listGuests rend les invités que le jeton voit, sur toute la grappe.
func (a *proxmoxAPI) listGuests(ctx context.Context) ([]pveGuest, error) {
	var guests []pveGuest
	err := a.call(ctx, http.MethodGet, "/cluster/resources?type=vm", nil, &guests)
	return guests, err
}

// guestStatus lit l'état courant d'un invité (running, stopped…).
func (a *proxmoxAPI) guestStatus(ctx context.Context, guest pveGuest) (string, error) {
	var current struct {
		Status string `json:"status"`
	}
	err := a.call(ctx, http.MethodGet, guestPath(guest)+"/status/current", nil, &current)
	return current.Status, err
}

// listSnapshots rend les noms des instantanés d'un invité.
func (a *proxmoxAPI) listSnapshots(ctx context.Context, guest pveGuest) ([]string, error) {
	var snapshots []struct {
		Name string `json:"name"`
	}
	if err := a.call(ctx, http.MethodGet, guestPath(guest)+"/snapshot", nil, &snapshots); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		names = append(names, snapshot.Name)
	}
	return names, nil
}

// startTask lance un geste qui rend une tâche et rend son identifiant.
func (a *proxmoxAPI) startTask(ctx context.Context, method, path string, form url.Values) (string, error) {
	var upid string
	if err := a.call(ctx, method, path, form, &upid); err != nil {
		return "", err
	}
	if !upidPattern.MatchString(upid) {
		return "", &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: "the answer is not a task identifier"}
	}
	return upid, nil
}

// taskState lit une tâche : en cours, ou finie avec son code de sortie.
func (a *proxmoxAPI) taskState(ctx context.Context, node, upid string) (running bool, exitStatus string, err error) {
	var task struct {
		Status     string `json:"status"`
		ExitStatus string `json:"exitstatus"`
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	if err := a.call(ctx, http.MethodGet, path, nil, &task); err != nil {
		return false, "", err
	}
	return task.Status != "stopped", task.ExitStatus, nil
}

// guestPath est le chemin d'un invité ; le nœud a été jugé (nodeNamePattern),
// la sorte et le numéro viennent d'un ordre validé.
func guestPath(guest pveGuest) string {
	return fmt.Sprintf("/nodes/%s/%s/%d", url.PathEscape(guest.Node), guest.Type, guest.VMID)
}

// call fait une requête et décode le champ data de la réponse.
func (a *proxmoxAPI) call(ctx context.Context, method, path string, form url.Values, into any) error {
	var body io.Reader
	if len(form) > 0 {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: "build request"}
	}
	request.Header.Set("Authorization", a.authorization)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := a.http.Do(request)
	if err != nil {
		return transportError(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maxProxmoxResponseBytes+1))
	if err != nil || len(content) > maxProxmoxResponseBytes {
		return &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: "unreadable or oversized answer"}
	}
	if failure := statusError(response.StatusCode, content); failure != nil {
		return failure
	}
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(content, &wrapped); err != nil || len(wrapped.Data) == 0 {
		return &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: "no data in the answer"}
	}
	if err := json.Unmarshal(wrapped.Data, into); err != nil {
		return &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: "unexpected data in the answer"}
	}
	return nil
}

// statusError range un statut HTTP : 401 et 403 sont un jeton refusé, tout ce
// qui n'est pas 200 une réponse inattendue (avec le message de Proxmox, borné).
func statusError(status int, content []byte) error {
	switch {
	case status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &pveError{reason: agentwire.ReasonProxmoxUnauthorized, message: fmt.Sprintf("status %d", status)}
	}
	message := fmt.Sprintf("status %d", status)
	var failure struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(content, &failure) == nil && failure.Message != "" {
		message += ": " + cutText(cleanLine(failure.Message), maxProxmoxMessageBytes)
	}
	return &pveError{reason: agentwire.ReasonProxmoxBadResponse, message: message}
}

// transportError distingue un certificat qui n'est pas celui de l'empreinte
// d'un hôte qui ne répond pas : c'est ce qui dit de reposer l'empreinte.
func transportError(err error) error {
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) || errors.Is(err, proxmox.ErrPinMismatch) {
		return &pveError{reason: agentwire.ReasonProxmoxCertificate, message: "the certificate is not the one of the fingerprint"}
	}
	return &pveError{reason: agentwire.ReasonProxmoxUnreachable, message: "the local Proxmox API did not answer"}
}

func cutText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}
