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
	total, online, err := s.machines.Count(r.Context())
	if err != nil {
		s.logger.Error("count machines", "error", err)
	}
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
		Nav:        s.navigation(text, machineCount{Total: total, Online: online, Known: err == nil}),
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

// Ce que la barre latérale sait des machines ; Known est faux quand le
// comptage a échoué, et rien n'est affiché plutôt qu'un zéro inventé.
type machineCount struct {
	Total  int
	Online int
	Known  bool
}

// Les autres compteurs viendront avec leurs composants (services…) ; le
// compteur des machines passe au rouge dès qu'une machine est hors ligne.
func (s *Server) navigation(text lang.Catalog, machines machineCount) []navSection {
	return []navSection{
		{
			Label: text.Get("nav.section_view"),
			Items: []navItem{
				{Key: navOverview, Label: text.Get("nav.overview"), Href: "/", Icon: "grid"},
				{Key: navMachines, Label: text.Get("nav.machines"), Href: "/machines", Icon: "server",
					Count: machines.Total, HasCount: machines.Known && machines.Total > 0, Hot: machines.Online < machines.Total},
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
