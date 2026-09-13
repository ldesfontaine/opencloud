package web

import (
	"embed"
	"io/fs"
)

// Le front compilé par Vite, tel quel : index.html, assets/ aux noms
// empreintés, et le contenu de public/ (favicon, theme.js). Le Makefile
// garde dist/.gitkeep : go:embed exige un dossier non vide.
//
//go:embed all:dist
var files embed.FS

func Dist() (fs.FS, error) {
	return fs.Sub(files, "dist")
}
