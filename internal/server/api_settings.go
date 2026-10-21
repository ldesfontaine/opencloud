package server

import (
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/alert"
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

func (s *Server) getDiskSettings(w http.ResponseWriter, r *http.Request) {
	thresholds, err := s.settings.DiskThresholds(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, diskSettingsToJSON(thresholds))
}

func (s *Server) setDiskSettings(w http.ResponseWriter, r *http.Request) {
	var request diskSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	thresholds := alert.DiskThresholds{Attention: request.Attention, Danger: request.Danger}
	err := s.settings.SetDiskThresholds(r.Context(), thresholds)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, diskSettingsToJSON(thresholds))
}
