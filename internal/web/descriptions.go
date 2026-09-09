package web

import (
	"strings"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// Ce que l'interface dit d'une action avant qu'on la lance : une ligne, et la
// liste de ce que l'action va lire ou poser. Le catalogue reste un contrat
// d'exécution ; ces mots-là ne servent qu'à l'écran, ils vivent donc ici.
type actionDescription struct {
	// Une phrase, pas un paragraphe : elle tient sur la carte de l'action.
	Line string
	// Le titre de la liste : une action qui lit et une action qui pose ne
	// promettent pas la même chose.
	ItemsTitle string
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
var actionDescriptions = map[catalog.Kind]func() actionDescription{
	catalog.KindDiagnostiquer: diagnosticDescription,
	catalog.KindSocle:         socleDescription,
}

func describeAction(kind catalog.Kind) (actionDescription, bool) {
	describe, found := actionDescriptions[kind]
	if !found {
		return actionDescription{}, false
	}
	return describe(), true
}

func diagnosticDescription() actionDescription {
	return actionDescription{
		Line:       "Lit l'état de la machine sans rien changer.",
		ItemsTitle: "Ce qui va être lu",
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
	}
}

// La liste de paquets ne se recopie pas ici : elle vit dans
// internal/scripts/socle/packages.txt et l'écran la relit, sinon il mentirait
// dès qu'une ligne s'y ajoute.
func socleDescription() actionDescription {
	description := actionDescription{
		Line:       "Installe Docker et les outils de base, et crée les répertoires de /srv.",
		ItemsTitle: "Ce qui va être posé",
		Items: []describedItem{
			{Icon: iconCheck, Label: "les répertoires de la norme : /srv/workspace et /srv/data"},
			{Icon: iconKey, Label: "la source apt officielle de Docker et sa clé, versionnée dans le dépôt"},
		},
	}

	packages, err := scripts.Packages(string(catalog.KindSocle))
	if err != nil || len(packages) == 0 {
		return description
	}
	description.Items = append(description.Items, describedItem{
		Icon:  iconTerminal,
		Label: "les paquets de la liste versionnée : " + strings.Join(packages, ", "),
	})
	return description
}

// summaryOf : la ligne de l'interface quand elle existe, sinon le résumé du
// catalogue.
func summaryOf(definition catalog.Definition) string {
	if description, found := describeAction(definition.Kind); found {
		return description.Line
	}
	return definition.Summary
}
