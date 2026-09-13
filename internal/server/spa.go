package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
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
	if name != "" && name != indexFile && s.isAppFile(name) {
		if strings.HasPrefix(r.URL.Path, assetsPrefix) {
			w.Header().Set("Cache-Control", immutableYear)
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, s.app, name) // #nosec G703 -- name est passé par path.Clean et cherché dans un embed.FS clos sur dist : rien hors du binaire.
		return
	}
	if name != indexFile && path.Ext(name) != "" {
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
	if _, err := w.Write(index); err != nil {
		s.logger.Warn("write index", "path", r.URL.Path, "error", err)
	}
}

func (s *Server) isAppFile(name string) bool {
	info, err := fs.Stat(s.app, name)
	return err == nil && !info.IsDir()
}
