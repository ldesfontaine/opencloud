package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sort"
)

//go:embed static
var staticFiles embed.FS

const (
	staticPrefix  = "/static/"
	staticRoot    = "static"
	buildLength   = 12
	immutableYear = "public, max-age=31536000, immutable"
)

// Empreinte de tous les fichiers statiques : elle entre dans leurs URL, ce
// qui autorise un cache navigateur immuable sans jamais servir un CSS périmé.
func staticBuild() (string, error) {
	paths := make([]string, 0)
	err := fs.WalkDir(staticFiles, staticRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		content, err := staticFiles.ReadFile(path)
		if err != nil {
			return "", err
		}
		digest.Write([]byte(path))
		digest.Write(content)
	}
	return hex.EncodeToString(digest.Sum(nil))[:buildLength], nil
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if staticPrefix+r.PathValue("build")+"/" != s.staticBase {
		s.notFound(w, r)
		return
	}
	root, err := fs.Sub(staticFiles, staticRoot)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", immutableYear)
	r.URL.Path = "/" + r.PathValue("path")
	http.FileServerFS(root).ServeHTTP(w, r)
}
