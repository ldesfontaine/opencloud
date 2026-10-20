package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Les seuils d'alerte du disque, réglés sous Paramètres. Le retour n'est
// pas réglable : il suit l'attention, cinq points dessous.
type diskSettingsJSON struct {
	Attention int `json:"attention"`
	Danger    int `json:"danger"`
	Recovery  int `json:"recovery"`
	// Les bornes, pour que le formulaire dise la règle sans la recopier.
	MinAttention int `json:"min_attention"`
	MaxDanger    int `json:"max_danger"`
}

type diskSettingsRequest struct {
	Attention int `json:"attention"`
	Danger    int `json:"danger"`
}

func diskSettingsToJSON(thresholds alert.DiskThresholds) diskSettingsJSON {
	return diskSettingsJSON{
		Attention: thresholds.Attention, Danger: thresholds.Danger, Recovery: thresholds.Recovery(),
		MinAttention: alert.MinDiskAttentionPercent, MaxDanger: alert.MaxDiskDangerPercent,
	}
}

// diskThresholdsOf rend les seuils en vigueur : ceux du réglage, sinon ceux
// du produit.
func diskThresholdsOf(current settings.Settings) alert.DiskThresholds {
	return alert.DiskThresholdsOr(current.DiskAttentionPercent, current.DiskDangerPercent)
}

func (s *Server) getDiskSettings(w http.ResponseWriter, r *http.Request) {
	current, err := s.settings.LoadSettings(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, diskSettingsToJSON(diskThresholdsOf(current)))
}

func (s *Server) setDiskSettings(w http.ResponseWriter, r *http.Request) {
	var request diskSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	thresholds := alert.DiskThresholds{Attention: request.Attention, Danger: request.Danger}
	err := s.saveDiskThresholds(r.Context(), thresholds)
	if errors.Is(err, alert.ErrDiskThresholdsInvalid) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "settings.disk_invalid")
		return
	}
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, diskSettingsToJSON(thresholds))
}

// saveDiskThresholds ne touche que les deux seuils : la langue et la page
// de statut vivent sur la même ligne, et UpdateSettings les préserve.
func (s *Server) saveDiskThresholds(ctx context.Context, thresholds alert.DiskThresholds) error {
	if err := thresholds.Validate(); err != nil {
		return err
	}
	_, err := s.settings.UpdateSettings(ctx, func(current *settings.Settings) {
		current.DiskAttentionPercent = thresholds.Attention
		current.DiskDangerPercent = thresholds.Danger
	})
	return err
}
