package probe

import "time"

// Task est une sonde telle que l'agent l'exécute : ce qu'il faut pour
// sonder, et rien d'autre. Tous ses champs sont des valeurs, donc deux
// tâches se comparent : un jeu inchangé ne relance pas les goroutines.
type Task struct {
	ID              string `json:"id"`
	Kind            Kind   `json:"kind"`
	Target          string `json:"target"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Method          string `json:"method,omitempty"`
	ExpectedStatus  string `json:"expected_status,omitempty"`
	ExpectedBody    string `json:"expected_body,omitempty"`
	FollowRedirects bool   `json:"follow_redirects,omitempty"`
	// TLS : une sonde TCP fait une poignée de main au lieu d'une simple
	// connexion, pour lire le certificat d'un port qui ne parle pas HTTP.
	TLS bool `json:"tls,omitempty"`
}

func (t Task) interval() time.Duration {
	if t.IntervalSeconds <= 0 {
		return DefaultInterval
	}
	return time.Duration(t.IntervalSeconds) * time.Second
}

func (t Task) timeout() time.Duration {
	if t.TimeoutSeconds <= 0 {
		return DefaultTimeout
	}
	return time.Duration(t.TimeoutSeconds) * time.Second
}

func (t Task) method() string {
	if t.Method == "" {
		return "GET"
	}
	return t.Method
}

// Assignment est le jeu complet des sondes d'une machine : il remplace le
// précédent, une sonde absente s'arrête, une sonde en pause n'y est pas.
// Le serveur le pousse à l'ouverture du flux et à chaque changement ; une
// machine hors ligne le reçoit en revenant.
type Assignment struct {
	Probes []Task `json:"probes"`
}

// CommandProbes est le nom de l'événement du flux serveur vers agent.
const CommandProbes = "probes"

// Report est ce que l'agent met dans son signal, à côté des lectures de la
// machine et de ce que son Docker a montré.
type Report struct {
	Results []Result `json:"results,omitempty"`
}

func (r Report) IsEmpty() bool {
	return len(r.Results) == 0
}
