package server

import (
	"bytes"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	indexFile     = "index.html"
	assetsPrefix  = "/assets/"
	immutableYear = "public, max-age=31536000, immutable"
	// Le navigateur demande /favicon.ico de lui-même, même quand la page
	// déclare une autre icône ; on lui sert le signe de la marque plutôt
	// qu'un 404 dans les journaux. Le type dit SVG, et c'est lui qui compte.
	legacyFavicon = "favicon.ico"
	brandFavicon  = "favicon.svg"
	// La page de statut compilée par Vite, à côté d'index.html, sans une
	// ligne de l'administration ; le paquet public la sert.
	statusIndexFile = "statut.html"
)

// serveApp sert un fichier de dist s'il existe, sinon index.html : le
// routeur du navigateur prend la main sur le chemin. Un fichier demandé qui
// n'existe pas reste un 404, pas une page. Le motif n'a pas de méthode, pour
// ne pas se disputer /api/x avec le fourre-tout de l'API ; d'où le 405 à la main.
func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == legacyFavicon {
		name = brandFavicon
	}
	// Une page HTML ne se sert jamais comme un fichier : elle attend le nom
	// du produit, que seules ses routes (/ et /statut) lui donnent.
	if name != "" && path.Ext(name) != ".html" && s.isAppFile(name) {
		if strings.HasPrefix(r.URL.Path, assetsPrefix) {
			w.Header().Set("Cache-Control", immutableYear)
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, s.app, name) // #nosec G703 -- name est passé par path.Clean et cherché dans un embed.FS clos sur dist : rien hors du binaire.
		return
	}
	if name != indexFile && path.Ext(name) != "" {
		// statut.html demandé par son nom tombe ici : un 404, pas une page
		// sans nom de produit.
		http.NotFound(w, r)
		return
	}
	index, err := fs.ReadFile(s.app, indexFile)
	if err != nil {
		s.logger.Error("front not built", "error", err)
		http.Error(w, "front not built: run make front", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(expandProductName(index)); err != nil {
		s.logger.Warn("write index", "path", r.URL.Path, "error", err)
	}
}

// Le front est construit une fois et ne connaît pas le nom du produit : ses
// pages HTML le demandent par {{product}}, et le serveur le met à la
// livraison. Échappé, parce qu'il tombe dans une balise et un attribut.
func expandProductName(page []byte) []byte {
	return bytes.ReplaceAll(page, []byte("{{product}}"), []byte(html.EscapeString(brand.ProductName)))
}

// readStatusPage rend la page de statut du front compilé, nom du produit
// posé : le paquet public la sert sans connaître le front.
func (s *Server) readStatusPage() ([]byte, error) {
	page, err := fs.ReadFile(s.app, statusIndexFile)
	if err != nil {
		return nil, err
	}
	return expandProductName(page), nil
}

func (s *Server) isAppFile(name string) bool {
	info, err := fs.Stat(s.app, name)
	return err == nil && !info.IsDir()
}
