package server

import (
	"net/http"
	"strings"
)

// La méthode « * » : toutes, pour un fourre-tout.
const anyMethod = "*"

type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// L'arbre des routes, figé par routes_test.go : tout ajout casse le test,
// c'est voulu. Sous /api, ce que le front appelle, en JSON : une route par
// lecture, une route par action. Sous /agent, ce que l'agent d'une machine
// appelle ; sous /ping, ce qu'un cron appelle, sans authentification : le
// jeton est le secret. Tout le reste est le front, servi par spa.go.
func (s *Server) routes() []route {
	return []route{
		{"GET", "/api/session", s.session},
		{"PUT", "/api/session/language", s.setLanguage},
		{"GET", "/api/i18n/{code}", s.catalogAPI},
		{"GET", "/api/counts", s.counts},
		{"GET", "/api/events", s.events},
		{"GET", "/api/machines", s.listMachines},
		{"POST", "/api/machines/tokens", s.createMachineToken},
		{"DELETE", "/api/machines/tokens/{id}", s.cancelMachineToken},
		{"GET", "/api/machines/{id}", s.getMachine},
		{"DELETE", "/api/machines/{id}", s.removeMachine},
		{"POST", "/api/machines/{id}/actions/reenroll", s.reenrollMachine},
		{"GET", "/api/machines/{id}/resources", s.getMachineResources},
		{"GET", "/api/machines/{id}/resources/history", s.getMachineHistory},
		{"GET", "/api/resources", s.listResources},
		{"GET", "/api/machines/{id}/services", s.listMachineServices},
		{"GET", "/api/machines/{id}/network", s.getMachineNetwork},
		{"GET", "/api/network", s.listNetworks},
		{"GET", "/api/services", s.listServices},
		{"GET", "/api/services/{id}", s.getService},
		{"GET", "/api/services/{id}/transitions", s.listServiceTransitions},
		{"GET", "/api/services/{id}/logs", s.getServiceLogs},
		{"GET", "/api/services/{id}/logs/stream", s.streamServiceLogs},
		{"GET", "/api/jobs", s.listJobs},
		{"POST", "/api/jobs", s.createJob},
		{"GET", "/api/jobs/{id}", s.getJob},
		{"DELETE", "/api/jobs/{id}", s.deleteJob},
		{"POST", "/api/jobs/{id}/actions/pause", s.pauseJob},
		{"POST", "/api/jobs/{id}/actions/resume", s.resumeJob},
		{anyMethod, "/api/", s.apiUnknown},
		{"POST", "/agent/enroll", s.agentEnroll},
		{"POST", "/agent/challenge", s.agentChallenge},
		{"GET", "/agent/stream", s.agentStream},
		{"POST", "/agent/signal", s.agentSignal},
		{"POST", "/agent/logs/{request}", s.agentLogs},
		{"GET", "/ping/{token}", s.pingFinish},
		{"POST", "/ping/{token}", s.pingFinish},
		{"GET", "/ping/{token}/start", s.pingStart},
		{"POST", "/ping/{token}/start", s.pingStart},
		{"GET", "/ping/{token}/{code}", s.pingExitCode},
		{"POST", "/ping/{token}/{code}", s.pingExitCode},
		{anyMethod, "/", s.serveApp},
	}
}

// Toute écriture sous /api passe par la garde même-origine ; les lectures
// et les routes de l'agent et des pings n'en ont pas besoin.
func (s *Server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range s.routes() {
		handler := route.handler
		if strings.HasPrefix(route.pattern, "/api/") && route.method != http.MethodGet {
			handler = s.requireSameOrigin(handler)
		}
		pattern := route.pattern
		if route.method != anyMethod {
			pattern = route.method + " " + pattern
		}
		mux.HandleFunc(pattern, handler)
	}
	return mux
}

func (s *Server) apiUnknown(w http.ResponseWriter, _ *http.Request) {
	s.apiNotFound(w)
}
