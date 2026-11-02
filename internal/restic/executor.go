package restic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Une requête relayée ne tient pas rest-server plus longtemps que ceci : un
// paquet de restic fait quelques dizaines de Mio.
const maxRestServerCall = 15 * time.Minute

// RelayServer est ce que l'agent de la cible fait avec le serveur pour une
// requête relayée : tirer le corps, pousser la réponse. Il passe par la
// session de son flux ; ce paquet n'en sait rien.
type RelayServer interface {
	FetchBody(ctx context.Context, id string) (io.ReadCloser, error)
	SendResponse(ctx context.Context, id string, head agentwire.ResticRelayHead, body io.Reader) error
}

// PasswordOf rend le mot de passe REST de l'utilisateur d'un dépôt, que
// l'agent de la cible garde ; faux s'il n'en a pas.
type PasswordOf func(repo string) (string, bool)

// Executor rejoue côté cible une requête relayée sur rest-server, après
// l'avoir rejugée : la troisième barrière, si le relais était contourné ou
// compromis. C'est le même rest-server que le direct (--append-only
// --private-repos, htpasswd) : l'exécuteur s'y présente sous l'utilisateur du
// dépôt, et rest-server décide en dernier.
type Executor struct {
	restServer *url.URL
	client     *http.Client
	passwordOf PasswordOf
	logger     *slog.Logger
}

// NewExecutor : restServer est l'adresse de rest-server vue de la cible
// (https://127.0.0.1:<port>), client épingle son certificat comme seule
// racine.
func NewExecutor(restServer *url.URL, client *http.Client, passwordOf PasswordOf, logger *slog.Logger) *Executor {
	return &Executor{restServer: restServer, client: client, passwordOf: passwordOf, logger: logger}
}

// userUnknown : la cible n'a pas le mot de passe REST du dépôt nommé ; rien
// ne part à rest-server.
const userUnknown = "user_unknown"

// Handle sert une requête reçue par le flux, de bout en bout. Un refus part
// en 403 sans toucher rest-server ni tirer le corps.
func (e *Executor) Handle(ctx context.Context, server RelayServer, request agentwire.ResticRelayRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("relay request: %w", err)
	}
	if err := Allow(request.Method, request.Path, request.Query); err != nil {
		e.logger.Info("restic relay refused by target", "repo", request.Repo, "method", request.Method, "refusal", err.Error())
		return sendError(ctx, server, request.ID, http.StatusForbidden, err.Error())
	}
	password, isKnown := e.passwordOf(request.Repo)
	if !isKnown {
		e.logger.Info("restic relay refused by target", "repo", request.Repo, "refusal", userUnknown)
		return sendError(ctx, server, request.ID, http.StatusForbidden, userUnknown)
	}
	callContext, cancel := context.WithTimeout(ctx, maxRestServerCall)
	defer cancel()
	body, err := e.fetchBody(callContext, server, request)
	if err != nil {
		return sendError(ctx, server, request.ID, http.StatusBadGateway, "body_unavailable")
	}
	defer body.Close()
	response, err := e.callRestServer(callContext, request, password, body)
	if err != nil {
		e.logger.Warn("rest-server unreachable", "repo", request.Repo, "error", err)
		return sendError(ctx, server, request.ID, http.StatusBadGateway, "rest_server_unreachable")
	}
	defer response.Body.Close()
	return server.SendResponse(ctx, request.ID, headOf(response), response.Body)
}

// fetchBody ne tire un corps que pour un POST : les autres requêtes de
// restic n'en ont pas.
func (e *Executor) fetchBody(ctx context.Context, server RelayServer, request agentwire.ResticRelayRequest) (io.ReadCloser, error) {
	if request.Method != http.MethodPost || request.ContentLength == 0 {
		return http.NoBody, nil
	}
	return server.FetchBody(ctx, request.ID)
}

// callRestServer pose le dépôt devant le chemin : /<dépôt>/<chemin>. Le
// chemin vient d'être jugé par Allow, il n'a ni « .. » ni « % ». La requête
// porte l'utilisateur du dépôt : --private-repos ne l'ouvre qu'à lui.
func (e *Executor) callRestServer(ctx context.Context, request agentwire.ResticRelayRequest, password string, body io.Reader) (*http.Response, error) {
	location := *e.restServer
	location.Path = strings.TrimSuffix(location.Path, "/") + "/" + request.Repo + request.Path
	location.RawPath = ""
	location.RawQuery = request.Query
	outbound, err := http.NewRequestWithContext(ctx, request.Method, location.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build rest-server request: %w", err)
	}
	outbound.SetBasicAuth(request.Repo, password)
	if body != http.NoBody {
		outbound.ContentLength = request.ContentLength
	}
	if request.Accept != "" {
		outbound.Header.Set("Accept", request.Accept)
	}
	if request.Range != "" {
		outbound.Header.Set("Range", request.Range)
	}
	return e.client.Do(outbound)
}

func headOf(response *http.Response) agentwire.ResticRelayHead {
	head := agentwire.ResticRelayHead{Status: response.StatusCode, Headers: map[string]string{}}
	for _, name := range agentwire.ResticRelayResponseHeaders() {
		if value := response.Header.Get(name); value != "" {
			head.Headers[name] = value
		}
	}
	return head
}

func sendError(ctx context.Context, server RelayServer, id string, status int, code string) error {
	body := `{"error":"` + code + `"}`
	head := agentwire.ResticRelayHead{Status: status, Headers: map[string]string{
		"Content-Type":   "application/json",
		"Content-Length": strconv.Itoa(len(body)),
	}}
	return server.SendResponse(ctx, id, head, strings.NewReader(body))
}
