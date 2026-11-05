package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Une règle de seuil telle que le formulaire la lit : les valeurs, d'où elle
// vient, et les bornes pour que l'écran dise la règle sans la recopier. Le
// retour n'est pas réglable : il suit l'attention, cinq points dessous.
type ruleJSON struct {
	Resource      string `json:"resource"`
	Attention     int    `json:"attention"`
	Danger        int    `json:"danger"`
	Recovery      int    `json:"recovery"`
	WindowMinutes int    `json:"window_minutes"`
	// Origin : global, machine ou default. Sous Paramètres, toujours global
	// ou default ; sur la fiche d'une machine, machine quand elle porte sa
	// surcharge.
	Origin           string `json:"origin"`
	MinAttention     int    `json:"min_attention"`
	MaxDanger        int    `json:"max_danger"`
	MinWindowMinutes int    `json:"min_window_minutes"`
	MaxWindowMinutes int    `json:"max_window_minutes"`
	// Global : la règle que la machine suivrait sans sa surcharge ; absent
	// sous Paramètres.
	Global *ruleValuesJSON `json:"global,omitempty"`
}

type ruleValuesJSON struct {
	Attention     int `json:"attention"`
	Danger        int `json:"danger"`
	WindowMinutes int `json:"window_minutes"`
}

type rulesJSON struct {
	Rules []ruleJSON `json:"rules"`
}

type ruleRequest struct {
	Attention     int `json:"attention"`
	Danger        int `json:"danger"`
	WindowMinutes int `json:"window_minutes"`
}

func ruleToJSON(applied alert.Applied) ruleJSON {
	bounds := alert.BoundsOf(applied.Resource)
	return ruleJSON{
		Resource: string(applied.Resource), Attention: applied.Attention, Danger: applied.Danger, Recovery: applied.Recovery(),
		WindowMinutes: int(applied.Window.Minutes()), Origin: string(applied.Origin),
		MinAttention: bounds.MinAttention, MaxDanger: bounds.MaxDanger,
		MinWindowMinutes: int(bounds.MinWindow.Minutes()), MaxWindowMinutes: int(bounds.MaxWindow.Minutes()),
	}
}

func ruleFromRequest(resource alert.Resource, request ruleRequest) alert.Rule {
	return alert.Rule{
		Resource: resource, Attention: request.Attention, Danger: request.Danger,
		Window: time.Duration(request.WindowMinutes) * time.Minute,
	}
}

// Les seuils globaux, réglés sous Paramètres.
func (s *Server) getThresholds(w http.ResponseWriter, r *http.Request) {
	s.writeRules(w, r, "")
}

func (s *Server) setThreshold(w http.ResponseWriter, r *http.Request) {
	s.putRule(w, r, "")
}

// Les seuils d'une machine : sa surcharge quand elle en a une, sinon le
// réglage global.
func (s *Server) getMachineThresholds(w http.ResponseWriter, r *http.Request) {
	if _, err := s.machines.Get(r.Context(), r.PathValue("id")); s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeRules(w, r, r.PathValue("id"))
}

func (s *Server) setMachineThreshold(w http.ResponseWriter, r *http.Request) {
	s.putRule(w, r, r.PathValue("id"))
}

func (s *Server) clearMachineThreshold(w http.ResponseWriter, r *http.Request) {
	err := s.alerts.ClearRule(r.Context(), r.PathValue("id"), alert.Resource(r.PathValue("resource")))
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	s.writeRules(w, r, r.PathValue("id"))
}

func (s *Server) putRule(w http.ResponseWriter, r *http.Request, machineID string) {
	var request ruleRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	rule := ruleFromRequest(alert.Resource(r.PathValue("resource")), request)
	if s.failed(w, r, s.alerts.SetRule(r.Context(), machineID, rule), alert.ErrNotFound) {
		return
	}
	s.writeRules(w, r, machineID)
}

func (s *Server) writeRules(w http.ResponseWriter, r *http.Request, machineID string) {
	applied, err := s.alerts.Rules(r.Context(), machineID)
	if s.failed(w, r, err, nil) {
		return
	}
	body := rulesJSON{Rules: make([]ruleJSON, 0, len(applied))}
	for _, rule := range applied {
		body.Rules = append(body.Rules, ruleToJSON(rule))
	}
	if machineID != "" {
		global, err := s.alerts.Rules(r.Context(), "")
		if s.failed(w, r, err, nil) {
			return
		}
		for index := range body.Rules {
			body.Rules[index].Global = &ruleValuesJSON{
				Attention: global[index].Attention, Danger: global[index].Danger, WindowMinutes: int(global[index].Window.Minutes()),
			}
		}
	}
	s.writeAPI(w, http.StatusOK, body)
}

// Le rappel d'une alerte de danger restée sans lecture, en heures ; zéro le
// coupe.
type reminderSettingsJSON struct {
	Hours    int `json:"hours"`
	MaxHours int `json:"max_hours"`
}

type reminderSettingsRequest struct {
	Hours int `json:"hours"`
}

func (s *Server) getReminderSettings(w http.ResponseWriter, r *http.Request) {
	hours, err := s.settings.DangerReminderHours(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, reminderSettingsJSON{Hours: hours, MaxHours: alert.MaxReminderHours})
}

func (s *Server) setReminderSettings(w http.ResponseWriter, r *http.Request) {
	var request reminderSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	if s.failed(w, r, s.settings.SetDangerReminderHours(r.Context(), request.Hours), nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, reminderSettingsJSON{Hours: request.Hours, MaxHours: alert.MaxReminderHours})
}
