package web

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/ratelimit"
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
	pingSweepEvery     = 5 * time.Minute
	pingSweepIdle      = 10 * time.Minute
	retryAfterSeconds  = "2"
)

type pingLimits struct {
	bySource *ratelimit.Limiter
	byToken  *ratelimit.Limiter
	now      func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newPingLimits() *pingLimits {
	return &pingLimits{
		bySource: ratelimit.New(pingPerSourceRate, pingPerSourceBurst),
		byToken:  ratelimit.New(pingPerTokenRate, pingPerTokenBurst),
		now:      time.Now,
	}
}

func (l *pingLimits) setClock(now func() time.Time) {
	l.now = now
	l.bySource.SetClock(now)
	l.byToken.SetClock(now)
}

// allow compte la requête sur les deux seaux, l'IP d'abord : un jeton
// inconnu essayé en rafale s'arrête là, et son seau vide part au balayage
// suivant. Les seaux oubliés sont balayés au passage, sans goroutine.
func (l *pingLimits) allow(source, token string) bool {
	l.sweepIfDue()
	if !l.bySource.Allow(source) {
		return false
	}
	return l.byToken.Allow(token)
}

func (l *pingLimits) sweepIfDue() {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= pingSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.bySource.Sweep(pingSweepIdle)
		l.byToken.Sweep(pingSweepIdle)
	}
}

// GET ou POST /ping/{token} : la tâche a fini.
func (s *Server) pingFinish(w http.ResponseWriter, r *http.Request) {
	s.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindFinish})
}

// GET ou POST /ping/{token}/start : la tâche démarre.
func (s *Server) pingStart(w http.ResponseWriter, r *http.Request) {
	s.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindStart})
}

// GET ou POST /ping/{token}/{code} : la tâche a fini avec ce code de sortie.
// Le mux donne la priorité au segment littéral : « start » n'arrive pas ici.
// Le code est lu après la limitation, pour qu'un code faux ne soit pas
// une route illimitée ; hors bornes, le service le refuse.
func (s *Server) pingExitCode(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.Atoi(r.PathValue("code"))
	if err != nil {
		code = -1
	}
	s.receivePing(w, r, heartbeat.Ping{Kind: heartbeat.KindExitCode, ExitCode: &code})
}

// La réponse est du texte court, sans cookie ni JSON : un cron ne lit rien.
func (s *Server) receivePing(w http.ResponseWriter, r *http.Request, ping heartbeat.Ping) {
	token := r.PathValue("token")
	ping.Source = s.clientAddress(r)
	ping.Method = r.Method
	if !s.pingLimits.allow(ping.Source, token) {
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.writePing(w, http.StatusTooManyRequests, "too many pings")
		return
	}
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, heartbeat.MaxPayloadBytes))
		if err != nil {
			s.writePing(w, http.StatusBadRequest, "bad body")
			return
		}
		ping.Payload = string(body)
	}
	_, err := s.heartbeats.Receive(r.Context(), token, ping)
	switch {
	case errors.Is(err, heartbeat.ErrNotFound):
		s.writePing(w, http.StatusNotFound, "unknown token")
	case errors.Is(err, heartbeat.ErrExitCodeInvalid):
		s.writePing(w, http.StatusBadRequest, "bad exit code")
	case err != nil:
		s.logger.Error("receive ping", "error", err)
		s.writePing(w, http.StatusInternalServerError, "error")
	default:
		s.writePing(w, http.StatusOK, "ok")
	}
}

func (s *Server) writePing(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(text + "\n")); err != nil {
		s.logger.Warn("write ping response", "error", err)
	}
}
