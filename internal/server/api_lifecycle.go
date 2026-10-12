package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// La source de la fin de support, telle que Paramètres la nomme.
const lifecycleSourceName = "endoflife.date"

// Le support de l'image d'un service ou du système d'une machine : le
// produit reconnu, la branche qui tourne, jusqu'à quand elle est
// maintenue, et les branches maintenues au-dessus. Absent tant que le
// réglage est coupé.
type lifecycleJSON struct {
	Status     string           `json:"status"`
	Product    string           `json:"product"`
	ProductURL string           `json:"product_url"`
	Cycle      string           `json:"cycle"`
	EOLFrom    string           `json:"eol_from"`
	Latest     string           `json:"latest"`
	Newer      []newerCycleJSON `json:"newer"`
	FetchedAt  *time.Time       `json:"fetched_at"`
	IsStale    bool             `json:"is_stale"`
}

type newerCycleJSON struct {
	Cycle   string `json:"cycle"`
	Latest  string `json:"latest"`
	EOLFrom string `json:"eol_from"`
}

// Le réglage sous Paramètres : coupé par défaut, et ce que la dernière
// lecture de la source a donné.
type lifecycleSettingsJSON struct {
	Enabled       bool       `json:"enabled"`
	Source        string     `json:"source"`
	FetchedAt     *time.Time `json:"fetched_at"`
	Products      int        `json:"products"`
	IsFailing     bool       `json:"is_failing"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
}

type lifecycleSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

func lifecycleToJSON(verdict lifecycle.Verdict) *lifecycleJSON {
	converted := lifecycleJSON{
		Status: string(verdict.Status), Product: verdict.Product, ProductURL: verdict.ProductURL,
		Cycle: verdict.Cycle, EOLFrom: verdict.EOLFrom, Latest: verdict.Latest,
		Newer: make([]newerCycleJSON, 0, len(verdict.Newer)), FetchedAt: timeOrNil(verdict.FetchedAt), IsStale: verdict.IsStale,
	}
	for _, newer := range verdict.Newer {
		converted.Newer = append(converted.Newer, newerCycleJSON{Cycle: newer.Cycle, Latest: newer.Latest, EOLFrom: newer.EOLFrom})
	}
	return &converted
}

func (s *Server) lifecycleEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.LifecycleEnabled
}

// lifecycleOf rend le support de l'image d'un service, ou nil quand le
// réglage est coupé : la fiche ne montre alors rien.
func (s *Server) lifecycleOf(image string) *lifecycleJSON {
	if !s.lifecycleEnabled() {
		return nil
	}
	return lifecycleToJSON(s.lifecycle.Of(image))
}

func (s *Server) lifecycleSettings() lifecycleSettingsJSON {
	state := s.lifecycle.State()
	return lifecycleSettingsJSON{
		Enabled: s.lifecycleEnabled(), Source: lifecycleSourceName,
		FetchedAt: timeOrNil(state.FetchedAt), Products: state.Products,
		IsFailing: state.IsFailing, LastAttemptAt: timeOrNil(state.LastAttemptAt),
	}
}

func (s *Server) getLifecycleSettings(w http.ResponseWriter, _ *http.Request) {
	s.writeAPI(w, http.StatusOK, s.lifecycleSettings())
}

func (s *Server) setLifecycleSettings(w http.ResponseWriter, r *http.Request) {
	var request lifecycleSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	wasEnabled := s.lifecycleEnabled()
	if err := s.saveLifecycleEnabled(request.Enabled); err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	// Vient d'être activé : la première lecture part tout de suite, sans
	// attendre la cadence. Le redemander ne presse pas la source.
	if request.Enabled && !wasEnabled {
		s.lifecycle.Kick()
	}
	s.writeAPI(w, http.StatusOK, s.lifecycleSettings())
}

// saveLifecycleEnabled ne touche que sa clé : Update préserve le reste du
// fichier.
func (s *Server) saveLifecycleEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.settings.Update(func(current *settings.Settings) {
		current.LifecycleEnabled = enabled
	})
	if err != nil {
		return err
	}
	s.current = current
	return nil
}
