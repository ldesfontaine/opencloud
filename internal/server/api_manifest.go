package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

const manifestFileName = brand.ID + ".yaml"

// exportedDirectory écrit l'annuaire, commentaires dans la langue de
// l'interface : le paquet manifest n'importe pas les catalogues, il reçoit
// ses deux textes d'ici.
func (s *Server) exportedDirectory(ctx context.Context) ([]byte, error) {
	code, err := s.language(ctx)
	if err != nil {
		return nil, err
	}
	catalog := s.catalogs.For(code)
	return s.manifest.Export(ctx, manifest.Annotations{
		Header:      catalog.Get("manifest.export_header"),
		NeedsReview: catalog.Get("manifest.export_needs_review"),
	})
}

// exportManifest sert le fichier tel quel, en téléchargement : le même
// chemin pour le bouton de l'interface et pour la commande locale.
func (s *Server) exportManifest(w http.ResponseWriter, r *http.Request) {
	data, err := s.exportedDirectory(r.Context())
	if errors.Is(err, manifest.ErrExportTooLarge) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, manifest.KeyExportTooLarge)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+manifestFileName+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		s.logger.Warn("write manifest export", "error", err)
	}
}
