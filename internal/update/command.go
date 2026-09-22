package update

import (
	"strings"

	"github.com/ldesfontaine/opencloud/internal/service"
)

// Command fabrique ce que l'opérateur tapera pour appliquer la mise à
// jour ; openCloud ne l'exécute jamais. Un service Compose se tire et se
// recrée depuis son dossier ; un conteneur lancé à la main se tire
// seulement : le recréer demande ses options, qu'openCloud ne connaît
// pas. Vide sans mise à jour : la garde contre une rétrogradation est
// dans le constat, qui ne nomme qu'un tag strictement plus récent.
func Command(found service.Service, check Check) string {
	if !check.HasUpdate() {
		return ""
	}
	if found.ComposeService != "" && found.ComposeDir != "" {
		return "cd " + shellQuote(found.ComposeDir) + " && docker compose pull " + shellQuote(found.ComposeService) +
			" && docker compose up -d " + shellQuote(found.ComposeService)
	}
	return "docker pull " + shellQuote(check.Target())
}

// shellQuote entoure de guillemets simples ce qui en a besoin : un
// chemin avec une espace, un nom qui n'est pas qu'un mot.
func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, needsQuoting) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func needsQuoting(character rune) bool {
	switch {
	case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
		return false
	case character == '/', character == '.', character == '-', character == '_', character == ':', character == '@', character == '+':
		return false
	}
	return true
}
