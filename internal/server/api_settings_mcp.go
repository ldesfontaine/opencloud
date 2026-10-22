package server

import (
	"net"
	"net/http"
	"time"
)

// Ce que Paramètres dit de mcp-maison : s'il répond, sa version, combien
// d'outils openCloud il sert. SuggestedURL est ce qu'il faut écrire dans
// mcp_url (config.toml) pour qu'openCloud le cherche.
type mcpCheckResponse struct {
	URL            string    `json:"url"`
	State          string    `json:"state"`
	Version        string    `json:"version"`
	OpenCloudTools int       `json:"opencloud_tools"`
	CheckedAt      time.Time `json:"checked_at"`
	SuggestedURL   string    `json:"suggested_url"`
}

// getMCPCheck lit le /health de mcp-maison à chaque appel : la carte de
// Paramètres le demande à son ouverture et au bouton « Vérifier », sans
// boucle de fond ni alerte.
func (s *Server) getMCPCheck(w http.ResponseWriter, r *http.Request) {
	health := s.mcpCheck.Check(r.Context())
	host, _ := mcpEnvOf(s.listen)
	s.writeAPI(w, http.StatusOK, mcpCheckResponse{
		URL: s.mcpURL, State: string(health.State), Version: health.Version,
		OpenCloudTools: health.OpenCloudTools, CheckedAt: health.CheckedAt.UTC(),
		SuggestedURL: "http://" + net.JoinHostPort(host, mcpDefaultPort),
	})
}
