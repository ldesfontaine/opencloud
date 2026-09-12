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
		{"GET", "/machines", s.soon(navMachines, s.text.NavMachines)},
		{"GET", "/machines/nouvelle", s.soon(navMachines, s.text.NewMachineTitle)},
		{"GET", "/services", s.soon(navServices, s.text.NavServices)},
		{"GET", "/domaines", s.soon(navDomains, s.text.NavDomains)},
		{"GET", "/sauvegardes", s.soon(navBackups, s.text.NavBackups)},
		{"GET", "/alertes", s.soon(navAlerts, s.text.NavAlerts)},
		{"GET", "/parametres", s.soon(navSettings, s.text.NavSettings)},
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
