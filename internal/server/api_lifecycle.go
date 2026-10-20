package server

import (
	"context"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lifecycle"
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

// Le réglage sous Paramètres : activé tant que l'opérateur ne le coupe
// pas, et ce que la dernière lecture de la source a donné.
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
		Newer: convertAll(verdict.Newer, func(newer lifecycle.Newer) newerCycleJSON {
			return newerCycleJSON{Cycle: newer.Cycle, Latest: newer.Latest, EOLFrom: newer.EOLFrom}
		}),
		FetchedAt: timeOrNil(verdict.FetchedAt), IsStale: verdict.IsStale,
	}
	return &converted
}

// lifecycleEnabled relit le réglage dans la base : un autre processus a pu
// le changer.
func (s *Server) lifecycleEnabled(ctx context.Context) (bool, error) {
	return s.settings.LifecycleEnabled(ctx)
}

// lifecycleOf rend le support de l'image d'un service, ou nil quand le
// réglage est coupé : la fiche ne montre alors rien.
func (s *Server) lifecycleOf(ctx context.Context, image string) (*lifecycleJSON, error) {
	enabled, err := s.lifecycleEnabled(ctx)
	if err != nil || !enabled {
		return nil, err
	}
	return lifecycleToJSON(s.lifecycle.Of(image)), nil
}

func (s *Server) lifecycleSettings(enabled bool) lifecycleSettingsJSON {
	state := s.lifecycle.State()
	return lifecycleSettingsJSON{
		Enabled: enabled, Source: lifecycleSourceName,
		FetchedAt: timeOrNil(state.FetchedAt), Products: state.Products,
		IsFailing: state.IsFailing, LastAttemptAt: timeOrNil(state.LastAttemptAt),
	}
}

func (s *Server) getLifecycleSettings(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.lifecycleEnabled(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.lifecycleSettings(enabled))
}

func (s *Server) setLifecycleSettings(w http.ResponseWriter, r *http.Request) {
	var request lifecycleSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	isTurnedOn, err := s.settings.SetLifecycleEnabled(r.Context(), request.Enabled)
	if s.failed(w, r, err, nil) {
		return
	}
	// Vient d'être rallumé : une lecture part tout de suite, sans attendre
	// la cadence. Le redemander ne presse pas la source.
	if isTurnedOn {
		s.lifecycle.Kick()
	}
	s.writeAPI(w, http.StatusOK, s.lifecycleSettings(request.Enabled))
}
