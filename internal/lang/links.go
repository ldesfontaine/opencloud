package lang

import (
	"fmt"
	"strings"
)

// Un renvoi de menu est un marqueur du catalogue, {link:<id>}, jamais un
// chemin écrit en dur dans la phrase. Au chargement il devient un lien
// Markdown, [Paramètres → Notifications → Canaux](/parametres#canaux) :
// l'écran en fait un vrai lien, un client de l'API (mcp-maison) lit le chemin
// et l'adresse. Les libellés viennent des clés de menu de la langue : ils ne
// peuvent pas s'écarter de ce que l'écran affiche.

// Link décrit une cible : l'adresse de l'interface et les clés de catalogue
// des étapes du chemin, de la plus large à la plus fine.
type Link struct {
	ID    string
	Path  string
	Steps []string
}

var links = []Link{
	{"channels", "/parametres#canaux", []string{"nav.settings", "settings.section_notifications", "channel.title"}},
	{"api_tokens", "/parametres#jetons-api", []string{"nav.settings", "settings.section_ai", "api_token.title"}},
	{"mail", "/parametres#e-mails", []string{"nav.settings", "settings.section_mail"}},
	{"projects", "/services/projets", []string{"nav.services", "services.tab_projects"}},
}

// Links rend la liste fermée des cibles, pour le test de cohérence et pour
// le banc du front (qui vérifie que chaque adresse existe encore).
func Links() []Link {
	return append([]Link(nil), links...)
}

const linkOpen = "{link:"

// expandLinks remplace chaque {link:id} d'une valeur par son lien Markdown ;
// un identifiant inconnu refuse le démarrage, comme un marqueur inconnu.
func expandLinks(value string, catalog map[string]string) (string, error) {
	var out strings.Builder
	rest := value
	for {
		start := strings.Index(rest, linkOpen)
		if start < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			return "", fmt.Errorf("unterminated link marker in %q", value)
		}
		id := rest[start+len(linkOpen) : start+end]
		rendered, err := renderLink(id, catalog)
		if err != nil {
			return "", err
		}
		out.WriteString(rest[:start])
		out.WriteString(rendered)
		rest = rest[start+end+1:]
	}
}

func renderLink(id string, catalog map[string]string) (string, error) {
	for _, link := range links {
		if link.ID != id {
			continue
		}
		labels := make([]string, 0, len(link.Steps))
		for _, key := range link.Steps {
			label, ok := catalog[key]
			if !ok {
				return "", fmt.Errorf("link %s: missing key %s", id, key)
			}
			labels = append(labels, label)
		}
		return "[" + strings.Join(labels, " → ") + "](" + link.Path + ")", nil
	}
	return "", fmt.Errorf("unknown link %q", id)
}
