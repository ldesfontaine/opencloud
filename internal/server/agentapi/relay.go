package agentapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

const (
	// Un paquet de restic pèse quelques dizaines de Mio (128 au plus par
	// défaut) : au-delà, ce n'est pas restic qui envoie.
	maxRelayBody = 256 << 20
	// La tête de réponse de rest-server, en JSON : un statut et quatre
	// en-têtes.
	maxRelayHeadBytes = 2 << 10
	// restic ouvre cinq connexions par source et enchaîne ses requêtes : le
	// seau du relais, par adresse, en laisse passer des centaines par seconde
	// à quelques sources, et arrête qui essaie des preuves.
	relayPerSourceRate  = 200
	relayPerSourceBurst = 400
	relayIdle           = 10 * time.Minute
)

// relayLimiter borne les requêtes relayées par adresse, avant de lire une
// preuve : chaque essai coûte une lecture de la base et une signature.
func newRelayLimiter(clock func() time.Time) *ratelimit.BySource {
	limiter := ratelimit.NewBySource(relayPerSourceRate, relayPerSourceBurst, relayIdle)
	if clock != nil {
		limiter.SetClock(clock)
	}
	return limiter
}

// relayRest sert une requête de restic que la source relaie à sa cible : la
// preuve de la source (signée à chaque requête), le chemin tel qu'il est
// arrivé, encodé, jugé par le courtier (restic.Allow) ; la réponse de la
// cible repart au fil de l'eau.
func (h *Handler) relayRest(w http.ResponseWriter, r *http.Request) {
	if !h.speaksProtocol(w, r) {
		return
	}
	if !h.relayLimit.Allow(h.clientAddress(r)) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	if h.relay == nil {
		h.writeError(w, http.StatusServiceUnavailable, "relay_unavailable")
		return
	}
	proof, err := proofFromHeaders(r)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "bad_proof")
		return
	}
	response, err := h.relay.Submit(r.Context(), proof, r.PathValue("target"), restic.RelayRequest{
		Method: r.Method, Path: relayedPath(r), Query: r.URL.RawQuery,
		Accept: r.Header.Get("Accept"), Range: r.Header.Get("Range"),
		ContentLength: r.ContentLength, Body: http.MaxBytesReader(w, r.Body, maxRelayBody),
	})
	if err != nil {
		status, code := restic.RelayStatus(err)
		h.writeError(w, status, code)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		w.Header()[name] = values
	}
	w.WriteHeader(response.Status)
	_, _ = io.Copy(w, response.Body)
}

// relayedPath rend le chemin relatif au dépôt, encodé tel qu'il est arrivé :
// Allow refuse un « % » ou un « .. », qu'un chemin décodé cacherait.
func relayedPath(r *http.Request) string {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), agentwire.ResticRelayRoute)
	_, repoPath, _ := strings.Cut(rest, "/")
	return "/" + repoPath
}

// relayBody donne le corps d'une requête relayée à la cible qui la sert,
// sous le jeton de session de son flux.
func (h *Handler) relayBody(w http.ResponseWriter, r *http.Request) {
	targetID, ok := h.relaySession(w, r)
	if !ok {
		return
	}
	body, err := h.relay.TakeBody(targetID, r.PathValue("id"))
	if err != nil {
		status, code := restic.RelayStatus(err)
		h.writeError(w, status, code)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// relayResponse reçoit la réponse de rest-server que la cible pousse : la
// tête en en-tête, le corps au fil de l'eau.
func (h *Handler) relayResponse(w http.ResponseWriter, r *http.Request) {
	targetID, ok := h.relaySession(w, r)
	if !ok {
		return
	}
	encoded := r.Header.Get(agentwire.HeaderResticRelayHead)
	var head agentwire.ResticRelayHead
	if len(encoded) > maxRelayHeadBytes || json.Unmarshal([]byte(encoded), &head) != nil {
		h.writeError(w, http.StatusBadRequest, "bad_head")
		return
	}
	err := h.relay.Deliver(targetID, r.PathValue("id"), head, http.MaxBytesReader(w, r.Body, maxRelayBody))
	if errors.Is(err, restic.ErrUnknownRequest) {
		status, code := restic.RelayStatus(err)
		h.writeError(w, status, code)
		return
	}
	if err != nil {
		// La source est partie, ou la cible a coupé : rien à rendre de plus.
		h.logger.Warn("relay response", "machine_id", targetID, "error", err)
		h.writeError(w, http.StatusBadGateway, "relay_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// relaySession est la garde des routes de la cible : le protocole et la
// session de son flux. Elle rend la machine dont la session parle.
func (h *Handler) relaySession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !h.speaksProtocol(w, r) {
		return "", false
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return "", false
	}
	machineID, err := h.machines.SessionMachine(token)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return "", false
	}
	if h.relay == nil {
		h.writeError(w, http.StatusGone, "request_gone")
		return "", false
	}
	return machineID, true
}
