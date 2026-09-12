package web

import "github.com/ldesfontaine/opencloud/internal/lang"

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
	T          lang.Strings
	Version    string
	StaticBase string
	Title      string
	Subtitle   string
	Active     string
	Nav        []navSection
	Account    account
	Page       any
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

func (s *Server) newView(active, title, subtitle string) view {
	return view{
		T:          s.text,
		Version:    s.version,
		StaticBase: s.staticBase,
		Title:      title,
		Subtitle:   subtitle,
		Active:     active,
		Nav:        s.navigation(),
		Account:    s.account(),
	}
}

// Les compteurs viendront des composants (machines, services…) ; sans eux,
// aucun chiffre n'est affiché plutôt qu'un zéro inventé.
func (s *Server) navigation() []navSection {
	return []navSection{
		{
			Label: s.text.NavSectionView,
			Items: []navItem{
				{Key: navOverview, Label: s.text.NavOverview, Href: "/", Icon: "grid"},
				{Key: navMachines, Label: s.text.NavMachines, Href: "/machines", Icon: "server"},
				{Key: navServices, Label: s.text.NavServices, Href: "/services", Icon: "box"},
				{Key: navDomains, Label: s.text.NavDomains, Href: "/domaines", Icon: "globe"},
				{Key: navBackups, Label: s.text.NavBackups, Href: "/sauvegardes", Icon: "archive"},
				{Key: navAlerts, Label: s.text.NavAlerts, Href: "/alertes", Icon: "bell"},
			},
		},
		{
			Label: s.text.NavSectionSettings,
			Items: []navItem{
				{Key: navSettings, Label: s.text.NavSettings, Href: "/parametres", Icon: "settings"},
			},
		},
	}
}

// TODO(lucas): pas d'authentification dans le socle ; le compte est en dur.
func (s *Server) account() account {
	return account{
		Initial: "A",
		Name:    s.text.AccountName,
		Role:    s.text.AccountRole,
	}
}
