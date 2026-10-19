package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/ratelimit"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// La page de statut publique vit sous /statut, servie par le même binaire
// sur le même port, sans authentification : un visiteur ne connaît pas
// openCloud et ne voit que ce que les composants exposent. Toutes ses
// routes sont limitées par adresse résolue, comme /ping.
const (
	statusPath = "/statut"
	// La page compilée par Vite, à côté d'index.html, sans une ligne de
	// l'administration.
	statusIndexFile = "statut.html"
	// Les seules clés de catalogue qui sortent en public.
	statusKeyPrefix = "status."

	statusPerSourceRate  = 10
	statusPerSourceBurst = 20
	// Ouvrir un flux coûte plus qu'une lecture : une par seconde suffit à
	// un navigateur qui revient, cinq d'un coup à un cadre qui recharge.
	statusStreamRate  = 1
	statusStreamBurst = 5
	statusSweepEvery  = 5 * time.Minute
	statusSweepIdle   = 10 * time.Minute
	// Les visiteurs en direct, à part des onglets de l'administration.
	maxPublicSubscribers = 256
	// Ce qu'une même source (une IPv4, un /64 en IPv6) en tient à la fois :
	// quelques onglets et un cadre, pas le pool. Sans ce plafond, une adresse
	// qui ouvre un flux par seconde le vide en quatre minutes.
	maxPublicStreamsPerSource = 4
	// Un flux public se termine de lui-même : le navigateur se reconnecte
	// seul au bout de cinq secondes, et la place retourne au pool au lieu de
	// rester à une connexion que personne ne lit.
	maxPublicStreamLifetime = 10 * time.Minute
)

// isPublicStatusPage dit si le chemin est la page HTML publique, la seule
// réponse qui relâche frame-ancestors : c'est elle qu'un portail ou une
// documentation met dans un cadre, jamais son API ni l'administration.
func isPublicStatusPage(path string) bool {
	return path == statusPath || path == statusPath+"/"
}

type statusLimits struct {
	bySource *ratelimit.Limiter
	byStream *ratelimit.Limiter
	now      func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newStatusLimits() *statusLimits {
	return &statusLimits{
		bySource: ratelimit.New(statusPerSourceRate, statusPerSourceBurst),
		byStream: ratelimit.New(statusStreamRate, statusStreamBurst),
		now:      time.Now,
	}
}

func (l *statusLimits) setClock(now func() time.Time) {
	l.now = now
	l.bySource.SetClock(now)
	l.byStream.SetClock(now)
}

func (l *statusLimits) allowRead(source string) bool {
	l.sweepIfDue()
	return l.bySource.Allow(ratelimit.SourceKey(source))
}

func (l *statusLimits) allowStream(source string) bool {
	l.sweepIfDue()
	return l.byStream.Allow(ratelimit.SourceKey(source))
}

func (l *statusLimits) sweepIfDue() {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= statusSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.bySource.Sweep(statusSweepIdle)
		l.byStream.Sweep(statusSweepIdle)
	}
}

// limitPublic compte la requête sur le seau de sa source et répond 429
// au-delà ; le visiteur reçoit un mot, jamais une clé de catalogue.
func (s *Server) limitPublic(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.statusLimits.allowRead(s.clientAddress(r)) {
			s.writeTooMany(w)
			return
		}
		next(w, r)
	}
}

func (s *Server) writeTooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", retryAfterSeconds)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// GET /statut : la page, sans rien de l'administration.
func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	page, err := fs.ReadFile(s.app, statusIndexFile)
	if err != nil {
		s.logger.Error("status page not built", "error", err)
		http.Error(w, "front not built: run make front", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(expandProductName(page)); err != nil {
		s.logger.Warn("write status page", "error", err)
	}
}

// Tout autre chemin sous /statut est un 404 court : pas de page 404 de
// l'administration ici.
func (s *Server) statusUnknown(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

// Ce que le public lit : le titre, l'annonce, la langue, l'état global,
// les composants et les incidents. Jamais un identifiant d'objet, un nom
// de machine, de conteneur ou de sonde, une cible, un port. Un test le
// prouve sur la réponse réelle.
type publicStatusJSON struct {
	Title        string                `json:"title"`
	Announcement string                `json:"announcement"`
	Language     string                `json:"language"`
	GeneratedAt  time.Time             `json:"generated_at"`
	Global       string                `json:"global"`
	Components   []publicComponentJSON `json:"components"`
	Open         []publicIncidentJSON  `json:"open"`
	History      []publicIncidentJSON  `json:"history"`
}

type publicComponentJSON struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	State string          `json:"state"`
	Days  []statusDayJSON `json:"days"`
}

// Un jour d'uptime d'un composant : des comptes, jamais un pourcentage.
type statusDayJSON struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Success  int    `json:"success"`
	Degraded int    `json:"degraded"`
}

type publicIncidentJSON struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Impact     string       `json:"impact"`
	Status     string       `json:"status"`
	StartsAt   *time.Time   `json:"starts_at"`
	EndsAt     *time.Time   `json:"ends_at"`
	Components []string     `json:"components"`
	Updates    []updateJSON `json:"updates"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	ResolvedAt *time.Time   `json:"resolved_at"`
}

type updateJSON struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// GET /statut/api/status
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.status.Snapshot(r.Context())
	if err != nil {
		s.logger.Error("status snapshot", "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	s.writeAPI(w, http.StatusOK, snapshotToJSON(snapshot))
}

func snapshotToJSON(snapshot status.Snapshot) publicStatusJSON {
	response := publicStatusJSON{
		Title:        snapshot.Page.Title,
		Announcement: snapshot.Page.Announcement,
		Language:     string(snapshot.Page.Language),
		GeneratedAt:  snapshot.GeneratedAt.UTC(),
		Global:       string(snapshot.Global),
		Components:   make([]publicComponentJSON, 0, len(snapshot.Components)),
		Open:         make([]publicIncidentJSON, 0, len(snapshot.Open)),
		History:      make([]publicIncidentJSON, 0, len(snapshot.History)),
	}
	for _, component := range snapshot.Components {
		days := make([]statusDayJSON, 0, len(component.Days))
		for _, day := range component.Days {
			days = append(days, statusDayJSON{Day: day.Day.UTC().Format(time.DateOnly), Total: day.Total, Success: day.Success, Degraded: day.Degraded})
		}
		response.Components = append(response.Components, publicComponentJSON{ID: component.ID, Name: component.Name, State: string(component.State), Days: days})
	}
	for _, incident := range snapshot.Open {
		response.Open = append(response.Open, publicIncidentToJSON(incident))
	}
	for _, incident := range snapshot.History {
		response.History = append(response.History, publicIncidentToJSON(incident))
	}
	return response
}

func publicIncidentToJSON(incident status.Incident) publicIncidentJSON {
	response := publicIncidentJSON{
		ID:         incident.ID,
		Title:      incident.Title,
		Impact:     string(incident.Impact),
		Status:     string(incident.Status),
		StartsAt:   timeOrNil(incident.StartsAt),
		EndsAt:     timeOrNil(incident.EndsAt),
		Components: make([]string, 0, len(incident.Components)),
		Updates:    updatesToJSON(incident.Updates),
		CreatedAt:  incident.CreatedAt.UTC(),
		UpdatedAt:  incident.UpdatedAt.UTC(),
		ResolvedAt: timeOrNil(incident.ResolvedAt),
	}
	for _, component := range incident.Components {
		response.Components = append(response.Components, component.Name)
	}
	return response
}

func updatesToJSON(updates []status.Update) []updateJSON {
	response := make([]updateJSON, 0, len(updates))
	for _, update := range updates {
		response = append(response, updateJSON{Status: string(update.Status), Message: update.Message, CreatedAt: update.CreatedAt.UTC()})
	}
	return response
}

// GET /statut/api/i18n : les chaînes de la page dans sa langue, et rien
// d'autre du catalogue.
func (s *Server) publicCatalog(w http.ResponseWriter, r *http.Request) {
	page, err := s.status.Page(r.Context())
	if err != nil {
		s.logger.Error("status page settings", "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	catalog := s.catalogs.For(page.Language)
	strings := map[string]string{}
	for _, key := range catalog.Keys() {
		if hasStatusPrefix(key) {
			strings[key] = catalog.Get(key)
		}
	}
	s.writeAPI(w, http.StatusOK, strings)
}

func hasStatusPrefix(key string) bool {
	return strings.HasPrefix(key, statusKeyPrefix)
}

// GET /statut/api/events : le direct public, sur son propre bus, qui ne
// porte que « status ». Plafonné à part des onglets de l'administration, par
// source d'abord : la part d'une source se vérifie avant de toucher au pool
// global, pour qu'un refus ne coûte une place à personne.
func (s *Server) publicEvents(w http.ResponseWriter, r *http.Request) {
	address := s.clientAddress(r)
	if !s.statusLimits.allowStream(address) {
		s.writeTooMany(w)
		return
	}
	source := ratelimit.SourceKey(address)
	if !s.publicStreamsBySource.take(source) {
		s.logger.Warn("public status streams limit reached for this source", "source", address)
		s.writeTooMany(w)
		return
	}
	defer s.publicStreamsBySource.release(source)
	if !s.publicTabs.take() {
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	defer s.publicTabs.release()
	// Fin propre à l'échéance : la réponse se termine, EventSource se reconnecte.
	ctx, cancel := context.WithTimeout(r.Context(), s.publicStreamLifetime)
	defer cancel()
	s.streamTopics(w, r.WithContext(ctx), s.publicLive)
}
