package lang

type Strings struct {
	AppName string

	NavSectionView     string
	NavSectionSettings string
	NavOverview        string
	NavMachines        string
	NavServices        string
	NavDomains         string
	NavBackups         string
	NavAlerts          string
	NavSettings        string

	ThemeLight  string
	ThemeDark   string
	AccountName string
	AccountRole string

	OverviewSubtitle   string
	OverviewEmptyTitle string
	OverviewEmptyText  string
	AddMachine         string
	NewMachineTitle    string

	SoonSubtitle string
	SoonBadge    string
	SoonText     string

	NotFoundTitle    string
	NotFoundSubtitle string
	NotFoundText     string
	BackToOverview   string

	InternalError string

	VisualSystemTitle    string
	VisualSystemSubtitle string
}

func French() Strings {
	return Strings{
		AppName: "openCloud",

		NavSectionView:     "Vue",
		NavSectionSettings: "Réglages",
		NavOverview:        "Vue d'ensemble",
		NavMachines:        "Machines",
		NavServices:        "Services",
		NavDomains:         "Domaines",
		NavBackups:         "Sauvegardes",
		NavAlerts:          "Alertes",
		NavSettings:        "Paramètres",

		ThemeLight:  "Thème clair",
		ThemeDark:   "Thème sombre",
		AccountName: "Administrateur",
		AccountRole: "Compte local",

		OverviewSubtitle:   "Aucune machine pour l'instant.",
		OverviewEmptyTitle: "Aucune machine",
		OverviewEmptyText:  "Ajoutez une première machine pour voir ici son état, ses services et ses alertes.",
		AddMachine:         "Ajouter une machine",
		NewMachineTitle:    "Nouvelle machine",

		SoonSubtitle: "Cette page arrive avec une prochaine fonctionnalité.",
		SoonBadge:    "Bientôt",
		SoonText:     "Le socle est en place ; le contenu suivra, fonctionnalité par fonctionnalité.",

		NotFoundTitle:    "Page introuvable",
		NotFoundSubtitle: "Cette adresse ne correspond à rien.",
		NotFoundText:     "Vérifiez le lien ou revenez à la vue d'ensemble.",
		BackToOverview:   "Revenir à la vue d'ensemble",

		InternalError: "Une erreur interne est survenue. Consultez les journaux du serveur.",

		VisualSystemTitle:    "Système visuel",
		VisualSystemSubtitle: "Une seule couleur d'accent, du noir pour l'action principale, des cartes bordées sans ombre. Geist pour lire, Geist Mono pour ce qui se copie.",
	}
}
