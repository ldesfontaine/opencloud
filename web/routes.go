package web

import "net/http"

type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// L'arbre des routes, figé par routes_test.go : tout ajout casse le test,
// c'est voulu. Une route par écran, une route par action.
func (s *Server) routes() []route {
	return []route{
		{"GET", "/{$}", s.overview},
		{"GET", "/machines", s.soon(navMachines, "nav.machines")},
		{"GET", "/machines/nouvelle", s.soon(navMachines, "machine.new_title")},
		{"GET", "/services", s.soon(navServices, "nav.services")},
		{"GET", "/domaines", s.soon(navDomains, "nav.domains")},
		{"GET", "/sauvegardes", s.soon(navBackups, "nav.backups")},
		{"GET", "/alertes", s.soon(navAlerts, "nav.alerts")},
		{"GET", "/parametres", s.soon(navSettings, "nav.settings")},
		{"POST", "/langue", s.setLanguage},
		{"GET", "/systeme-visuel", s.visualSystem},
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
