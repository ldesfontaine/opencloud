package web

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Clés des entrées de navigation ; l'entrée active se choisit par clé.
const (
	navOverview = "overview"
	navMachines = "machines"
	navServices = "services"
	navDomains  = "domains"
	navBackups  = "backups"
	navAlerts   = "alerts"
	navSettings = "settings"
)

// Ce que reçoit chaque gabarit : la coquille commune plus la page.
type view struct {
	T          lang.Catalog
	Lang       string
	Languages  []languageOption
	CSRF       string
	Return     string
	Version    string
	StaticBase string
	Title      string
	Subtitle   string
	Active     string
	Nav        []navSection
	Account    account
	Page       any
}

type languageOption struct {
	Code   string
	Label  string
	Active bool
}

type navSection struct {
	Label string
	Items []navItem
}

type navItem struct {
	Key      string
	Label    string
	Href     string
	Icon     string
	Count    int
	HasCount bool
	Hot      bool
}

type account struct {
	Initial string
	Name    string
	Role    string
}

func (s *Server) newView(r *http.Request, active, title, subtitle string) view {
	text := s.catalog()
	return view{
		T:          text,
		Lang:       string(text.Code()),
		Languages:  s.languages(text),
		CSRF:       csrfToken(r),
		Return:     r.URL.RequestURI(),
		Version:    s.version,
		StaticBase: s.staticBase,
		Title:      title,
		Subtitle:   subtitle,
		Active:     active,
		Nav:        s.navigation(text),
		Account:    s.account(text),
	}
}

func (s *Server) languages(text lang.Catalog) []languageOption {
	options := make([]languageOption, 0, len(lang.Codes()))
	for _, code := range lang.Codes() {
		options = append(options, languageOption{
			Code:   string(code),
			Label:  text.Get("language." + string(code)),
			Active: code == text.Code(),
		})
	}
	return options
}

// Les compteurs viendront des composants (machines, services…) ; sans eux,
// aucun chiffre n'est affiché plutôt qu'un zéro inventé.
func (s *Server) navigation(text lang.Catalog) []navSection {
	return []navSection{
		{
			Label: text.Get("nav.section_view"),
			Items: []navItem{
				{Key: navOverview, Label: text.Get("nav.overview"), Href: "/", Icon: "grid"},
				{Key: navMachines, Label: text.Get("nav.machines"), Href: "/machines", Icon: "server"},
				{Key: navServices, Label: text.Get("nav.services"), Href: "/services", Icon: "box"},
				{Key: navDomains, Label: text.Get("nav.domains"), Href: "/domaines", Icon: "globe"},
				{Key: navBackups, Label: text.Get("nav.backups"), Href: "/sauvegardes", Icon: "archive"},
				{Key: navAlerts, Label: text.Get("nav.alerts"), Href: "/alertes", Icon: "bell"},
			},
		},
		{
			Label: text.Get("nav.section_settings"),
			Items: []navItem{
				{Key: navSettings, Label: text.Get("nav.settings"), Href: "/parametres", Icon: "settings"},
			},
		},
	}
}

// TODO(lucas): pas d'authentification dans le socle ; le compte est en dur.
func (s *Server) account(text lang.Catalog) account {
	return account{
		Initial: "A",
		Name:    text.Get("account.name"),
		Role:    text.Get("account.role"),
	}
}
