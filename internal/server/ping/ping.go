package ping

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Les bornes du débit sur /ping : par IP source résolue, pour freiner
// l'énumération de jetons, et par jeton, pour qu'un jeton fuité ne
// remplisse pas la base. Derrière Traefik sans trusted_proxies, tous les
// pings semblent venir de la boucle locale et partagent un seul seau.
const (
	pingPerSourceRate  = 10
	pingPerSourceBurst = 20
	pingPerTokenRate   = 0.5
	pingPerTokenBurst  = 30
	pingIdle           = 10 * time.Minute
	// Un vrai jeton fait 29 octets ; la marge laisse passer les jetons hérités
	// en clair, dont le format n'a jamais été garanti. Au-delà, le jeton ne
	// peut pas exister : il est refusé sans rien retenir.
	maxPingTokenLength = 256
)

// allowPing compte la requête sur les deux seaux, la source d'abord : un
// jeton inconnu essayé en rafale s'arrête là, et son seau vide part au
// balayage suivant.
//
// Le seau par jeton est indexé par l'empreinte du jeton : le jeton vient de
// l'URL, un anonyme l'invente, et sa clé reste en mémoire jusqu'au balayage.
// Une empreinte pèse toujours la même chose, quelle que soit la requête. Un
// jeton trop long ne crée aucun seau : il est compté sur la source seule, et
// receivePing le refuse.
func (h *Handler) allowPing(source, token string) bool {
	if !h.sourceLimit.Allow(source) {
		return false
	}
	if len(token) > maxPingTokenLength {
		return true
	}
	return h.tokenLimit.Allow(heartbeat.HashToken(token))
}

// GET ou POST /ping/{token} : la tâche a fini.
func (h *Handler) finish(w http.ResponseWriter, r *http.Request) {
	h.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindFinish})
}

// GET ou POST /ping/{token}/start : la tâche démarre.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	h.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindStart})
}

// GET ou POST /ping/{token}/{code} : la tâche a fini avec ce code de sortie.
// Le mux donne la priorité au segment littéral : « start » n'arrive pas ici.
// Le code est lu après la limitation, pour qu'un code faux ne soit pas
// une route illimitée ; hors bornes, le service le refuse.
func (h *Handler) exitCode(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.PathValue("code"))
	if err != nil {
		code = -1
	}
	h.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &code})
}

// La réponse est du texte court, sans cookie ni JSON : un cron ne lit rien.
func (h *Handler) receivePing(w http.ResponseWriter, r *http.Request, ping heartbeat.Ping) {
	token := r.PathValue("token")
	ping.Source = h.clientAddress(r)
	ping.Method = r.Method
	if !h.allowPing(ping.Source, token) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writePing(w, http.StatusTooManyRequests, "too many pings")
		return
	}
	if len(token) > maxPingTokenLength {
		h.writePing(w, http.StatusNotFound, "unknown token")
		return
	}
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, heartbeat.MaxPayloadBytes))
		if err != nil {
			h.writePing(w, http.StatusBadRequest, "bad body")
			return
		}
		ping.Payload = string(body)
	}
	_, err := h.heartbeats.Receive(r.Context(), token, ping)
	switch {
	case errors.Is(err, heartbeat.ErrNotFound):
		h.writePing(w, http.StatusNotFound, "unknown token")
	case errors.Is(err, heartbeat.ErrExitCodeInvalid):
		h.writePing(w, http.StatusBadRequest, "bad exit code")
	case err != nil:
		h.logger.Error("receive ping", "error", err)
		h.writePing(w, http.StatusInternalServerError, "error")
	default:
		h.writePing(w, http.StatusOK, "ok")
	}
}

func (h *Handler) writePing(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(text + "\n")); err != nil {
		h.logger.Warn("write ping response", "error", err)
	}
}
