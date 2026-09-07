package web

import "github.com/ldesfontaine/opencloud/internal/catalog"

// Ce que l'interface dit d'une action avant qu'on la lance : une ligne, et la
// liste de ce que l'action va lire ou poser. Le catalogue reste un contrat
// d'exécution ; ces mots-là ne servent qu'à l'écran, ils vivent donc ici.
type actionDescription struct {
	// Une phrase, pas un paragraphe : elle tient sur la carte de l'action.
	Line string
	// Ce qui va être lu ou posé, un item par ligne, icône devant.
	Items []describedItem
}

// describedItem : un item de la liste, et le nom de son icône — celui du
// gabarit, sans le préfixe « icon- ».
type describedItem struct {
	Icon  string
	Label string
}

// Une action sans description garde le résumé du catalogue et n'affiche
// aucune liste : la table dit ce qu'on sait dire, pas ce qu'on promet.
var actionDescriptions = map[catalog.Kind]actionDescription{
	catalog.KindDiagnostiquer: {
		Line: "Lit l'état de la machine sans rien changer.",
		Items: []describedItem{
			{Icon: iconServer, Label: "le système : nom, noyau, distribution"},
			{Icon: iconClock, Label: "l'horloge et depuis quand la machine est allumée"},
			{Icon: iconActivity, Label: "l'espace libre et la mémoire"},
			{Icon: iconRefresh, Label: "les unités systemd en échec"},
			{Icon: iconTerminal, Label: "Docker et son plugin compose"},
			{Icon: iconShield, Label: "les ports 80 et 443"},
			{Icon: iconCheck, Label: "la norme /srv : workspace et data"},
			{Icon: iconKey, Label: "le lanceur : propriétaire, mode, empreinte"},
		},
	},
}

func describeAction(kind catalog.Kind) (actionDescription, bool) {
	description, found := actionDescriptions[kind]
	return description, found
}

// summaryOf : la ligne de l'interface quand elle existe, sinon le résumé du
// catalogue.
func summaryOf(definition catalog.Definition) string {
	if description, found := describeAction(definition.Kind); found {
		return description.Line
	}
	return definition.Summary
}
