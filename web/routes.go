package web

import "net/http"

type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// L'arbre des routes, figé par routes_test.go : tout ajout casse le test,
// c'est voulu. Une route par écran, une route par action ; les fragments
// HTMX portent un nom de morceau (tableau, en-tete). Sous /agent, ce que
// l'agent d'une machine appelle.
func (s *Server) routes() []route {
	return []route{
		{"GET", "/{$}", s.overview},
		{"GET", "/machines", s.machinesPage},
		{"GET", "/machines/tableau", s.machinesTable},
		{"GET", "/machines/nouvelle", s.newMachinePage},
		{"POST", "/machines/nouvelle", s.createMachineToken},
		{"POST", "/machines/jetons/{id}/actions/annuler", s.cancelMachineToken},
		{"GET", "/machines/{id}", s.machinePage},
		{"GET", "/machines/{id}/en-tete", s.machineHead},
		{"GET", "/machines/{id}/{tab}", s.machinePage},
		{"POST", "/machines/{id}/actions/retirer", s.removeMachine},
		{"POST", "/machines/{id}/actions/reenroler", s.reenrollMachine},
		{"GET", "/services", s.soon(navServices, "nav.services")},
		{"GET", "/domaines", s.soon(navDomains, "nav.domains")},
		{"GET", "/sauvegardes", s.soon(navBackups, "nav.backups")},
		{"GET", "/alertes", s.soon(navAlerts, "nav.alerts")},
		{"GET", "/parametres", s.soon(navSettings, "nav.settings")},
		{"POST", "/langue", s.setLanguage},
		{"GET", "/systeme-visuel", s.visualSystem},
		{"POST", "/agent/enroll", s.agentEnroll},
		{"POST", "/agent/challenge", s.agentChallenge},
		{"GET", "/agent/stream", s.agentStream},
		{"POST", "/agent/signal", s.agentSignal},
		{"GET", staticPrefix + "{build}/{path...}", s.static},
		{"GET", "/", s.notFound},
	}
}

func (s *Server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range s.routes() {
		mux.HandleFunc(route.method+" "+route.pattern, route.handler)
	}
	return mux
}
