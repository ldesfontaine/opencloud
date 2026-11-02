package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Les requêtes relayées qu'une cible sert en même temps : restic en ouvre
// cinq par source. Au-delà, 503 : restic réessaie, le mandataire de la
// source borne sa patience.
const maxRelayInFlight = 8

// Les codes qu'une cible rend sans toucher rest-server.
const (
	relayRightMissing   = "right_missing"
	relayTargetNotReady = "target_not_ready"
	relayTargetBusy     = "target_busy"
)

// serveRelay sert une requête relayée par le serveur : la cible la rejuge
// (restic.Executor) et la rejoue sur son rest-server, en TLS épinglé, sous
// l'utilisateur du dépôt. Le travail se fait à part : le flux continue de
// lire les événements.
func (t *resticTarget) serveRelay(ctx context.Context, server restic.RelayServer, request agentwire.ResticRelayRequest) {
	relay, ok := t.relayTarget()
	if !ok {
		t.answerRelay(ctx, server, request.ID, http.StatusServiceUnavailable, relayTargetNotReady)
		return
	}
	select {
	case t.relaySlots <- struct{}{}:
	default:
		t.answerRelay(ctx, server, request.ID, http.StatusServiceUnavailable, relayTargetBusy)
		return
	}
	t.noteRelayed(request)
	executor := restic.NewExecutor(relay.address, relay.client, t.relayPassword, t.logger)
	go func() {
		defer func() { <-t.relaySlots }()
		if err := executor.Handle(ctx, server, request); err != nil {
			t.logger.Warn("restic relay request", "repo", request.Repo, "method", request.Method, "error", err)
		}
	}()
}

// answerRelay répond à une requête sans toucher rest-server, par le même
// chemin qu'une réponse : {"error": code}.
func (t *resticTarget) answerRelay(ctx context.Context, server restic.RelayServer, id string, status int, code string) {
	body := `{"error":"` + code + `"}`
	head := agentwire.ResticRelayHead{Status: status, Headers: map[string]string{
		"Content-Type":   "application/json",
		"Content-Length": strconv.Itoa(len(body)),
	}}
	if err := server.SendResponse(ctx, id, head, strings.NewReader(body)); err != nil {
		t.logger.Warn("restic relay answer", "code", code, "error", err)
	}
}

// relayLink est le chemin de la cible vers le relais du serveur : tirer le
// corps, pousser la réponse, sous le jeton de session du flux courant.
type relayLink struct {
	fetch *fetcher
}

func (l relayLink) FetchBody(ctx context.Context, id string) (io.ReadCloser, error) {
	client, session, err := l.fetch.current()
	if err != nil {
		return nil, err
	}
	return client.fetchRelayBody(ctx, session, id)
}

func (l relayLink) SendResponse(ctx context.Context, id string, head agentwire.ResticRelayHead, body io.Reader) error {
	client, session, err := l.fetch.current()
	if err != nil {
		return err
	}
	return client.sendRelayResponse(ctx, session, id, head, body)
}

// fetchRelayBody ouvre le corps d'une requête relayée ; à fermer. Pas de
// délai propre : l'exécuteur borne l'appel entier.
func (c *Client) fetchRelayBody(ctx context.Context, session, id string) (io.ReadCloser, error) {
	request, err := c.newRequest(ctx, http.MethodGet, agentwire.ResticRelayBodyPath(id), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch relay body: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, fmt.Errorf("fetch relay body: %w", serverError(response))
	}
	return response.Body, nil
}

// sendRelayResponse pousse la réponse de rest-server : la tête en en-tête,
// le corps au fil de l'eau.
func (c *Client) sendRelayResponse(ctx context.Context, session, id string, head agentwire.ResticRelayHead, body io.Reader) error {
	encodedHead, err := json.Marshal(head)
	if err != nil {
		return fmt.Errorf("encode relay head: %w", err)
	}
	request, err := c.newRequest(ctx, http.MethodPost, agentwire.ResticRelayResponsePath(id), io.NopCloser(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(agentwire.HeaderResticRelayHead, string(encodedHead))
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("send relay response: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("send relay response: %w", serverError(response))
	}
	return nil
}

// resticRelayConfig rend ce que le mandataire local de la source signe et
// où il envoie : le relais du serveur pour cette cible, par le client de
// l'agent (épinglé), sous la preuve de sa clé, une par requête.
func (c *Client) resticRelayConfig(identity Identity, target string) (restic.ProxyConfig, error) {
	endpoint, err := url.Parse(c.server + agentwire.ResticRelayPath(target))
	if err != nil {
		return restic.ProxyConfig{}, fmt.Errorf("relay endpoint: %w", err)
	}
	return restic.ProxyConfig{Endpoint: endpoint, Client: c.http, MachineID: identity.MachineID, Sign: identity.sign}, nil
}
