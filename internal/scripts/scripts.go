package scripts

import (
	"embed"
	"fmt"
	"path"
	"slices"
)

// L'en-tête commun et le corps de chaque action, un dossier par action.
//
//go:embed lib.sh */run.sh
var files embed.FS

const (
	// headerName tient les réglages et les fonctions de sortie ; bodyName le
	// corps d'une action, sans shebang : c'est l'en-tête qui le porte.
	headerName = "lib.sh"
	bodyName   = "run.sh"
)

// Script rend le fichier tel qu'il sera déposé : l'en-tête commun, puis le
// corps. Un seul fichier part sur la machine, il n'y a rien à y sourcer.
func Script(kind string) ([]byte, error) {
	header, err := files.ReadFile(headerName)
	if err != nil {
		return nil, fmt.Errorf("read script header: %w", err)
	}
	body, err := files.ReadFile(path.Join(kind, bodyName))
	if err != nil {
		return nil, fmt.Errorf("read script body for %q: %w", kind, err)
	}

	assembled := make([]byte, 0, len(header)+len(body))
	assembled = append(assembled, header...)
	return append(assembled, body...), nil
}

// Kinds rend les actions qui ont un script, dans un ordre stable. Le catalogue
// s'y compare : un dossier sans définition est une action qu'on ne voit pas.
func Kinds() []string {
	// La racine d'une FS embarquée se lit toujours : l'erreur n'existe pas ici.
	entries, _ := files.ReadDir(".")

	kinds := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			kinds = append(kinds, entry.Name())
		}
	}
	slices.Sort(kinds)
	return kinds
}
