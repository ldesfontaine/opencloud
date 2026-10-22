package probe

import (
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

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

// interval rend la cadence d'une sonde, jamais sous MinInterval : le
// serveur la valide déjà, l'agent ne fait pas confiance à ce qu'il reçoit
// pour sonder une cible toutes les secondes.
func (t Task) interval() time.Duration {
	if t.IntervalSeconds <= 0 {
		return DefaultInterval
	}
	return max(time.Duration(t.IntervalSeconds)*time.Second, MinInterval)
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

// Validate rejoue sur une tâche les bornes que le serveur applique à la
// création d'une sonde (Definition.Validate). Une tâche à zéro vaut les
// défauts, comme interval() et timeout() ; une méthode vide vaut GET. Aucune
// résolution de nom ici : un nom qui résout vers une adresse interdite est
// refusé à la composition (egress.Dial).
func (t Task) Validate() error {
	if !slices.Contains(Kinds(), t.Kind) {
		return ErrKindInvalid
	}
	if err := validateTarget(t.Kind, t.Target); err != nil {
		return err
	}
	if err := t.validateTiming(); err != nil {
		return err
	}
	if t.Kind != KindHTTP {
		return nil
	}
	return validateHTTPCheck(t.method(), t.ExpectedStatus, t.ExpectedBody)
}

// validateTiming compare en secondes : multiplier d'abord un entier venu du
// réseau par time.Second pourrait déborder.
func (t Task) validateTiming() error {
	if t.IntervalSeconds != 0 {
		seconds := time.Duration(t.IntervalSeconds)
		if seconds < MinInterval/time.Second || seconds > MaxInterval/time.Second {
			return ErrIntervalInvalid
		}
	}
	if t.TimeoutSeconds != 0 {
		seconds := time.Duration(t.TimeoutSeconds)
		if seconds < MinTimeout/time.Second || seconds > MaxTimeout/time.Second {
			return ErrTimeoutInvalid
		}
	}
	// Les valeurs effectives : un zéro vaut le défaut.
	if t.timeout() > t.interval() {
		return ErrTimeoutInvalid
	}
	return nil
}

// Assignment est le jeu complet des sondes d'une machine : il remplace le
// précédent, une sonde absente s'arrête, une sonde en pause n'y est pas.
// Le serveur le pousse à l'ouverture du flux et à chaque changement ; une
// machine hors ligne le reçoit en revenant.
type Assignment struct {
	Probes []Task `json:"probes"`
}

// Les raisons d'écarter une tâche qui ne tiennent pas à ses bornes. Ce ne
// sont pas des refus de l'opérateur : elles n'ont pas de catalogue, elles
// ne vont que dans le journal de l'agent.
const (
	DroppedIDInvalid  = "task.id_invalid"
	DroppedIDRepeated = "task.id_repeated"
	DroppedOverLimit  = "task.over_limit"
)

// Ce que Dropped garde d'une tâche écartée : assez pour la reconnaître dans
// le journal, rien que le serveur n'ait pu forger en longueur.
const (
	maxTaskIDLength      = 64
	maxDroppedKindLength = 16
	maxDroppedHostLength = 255
)

// Dropped dit pourquoi l'agent n'a pas appliqué une tâche. Host est l'hôte
// de la cible, jamais la cible entière : une URL peut porter un jeton en
// requête, et le journal ne le garde pas.
type Dropped struct {
	ID     string
	Kind   Kind
	Host   string
	Reason string
}

// Sanitize nettoie le jeu reçu avant qu'il soit appliqué. La commande
// `probes` n'est pas signée : l'agent ne fait pas confiance au jeu reçu plus
// qu'à un formulaire. Il garde l'ordre reçu, écarte une tâche invalide, un
// identifiant vide ou déjà vu, et tout ce qui dépasse MaxProbesPerMachine.
func Sanitize(tasks []Task) (kept []Task, dropped []Dropped) {
	seen := make(map[string]bool, min(len(tasks), MaxProbesPerMachine))
	for _, task := range tasks {
		if reason := judgeTask(task, seen, len(kept)); reason != "" {
			dropped = append(dropped, newDropped(task, reason))
			continue
		}
		seen[task.ID] = true
		kept = append(kept, task)
	}
	return kept, dropped
}

// judgeTask rend la raison d'écarter la tâche, ou rien. Un identifiant de
// plus de 64 caractères ferait refuser par le serveur chaque rapport qui le
// porte (validateResult) : autant ne pas le sonder.
func judgeTask(task Task, seen map[string]bool, keptCount int) string {
	if task.ID == "" || len(task.ID) > maxTaskIDLength {
		return DroppedIDInvalid
	}
	if err := task.Validate(); err != nil {
		key, _ := refusal.KeyOf(err)
		return key
	}
	if seen[task.ID] {
		return DroppedIDRepeated
	}
	if keptCount >= MaxProbesPerMachine {
		return DroppedOverLimit
	}
	return ""
}

func newDropped(task Task, reason string) Dropped {
	// Une cible illisible n'a pas d'hôte à montrer.
	address, _ := ParseTarget(task.Kind, task.Target)
	return Dropped{
		ID:     clip(task.ID, maxTaskIDLength),
		Kind:   Kind(clip(string(task.Kind), maxDroppedKindLength)),
		Host:   clip(address.Host, maxDroppedHostLength),
		Reason: reason,
	}
}

// clip coupe sans laisser un caractère à moitié.
func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}

// Report est ce que l'agent met dans son signal, à côté des lectures de la
// machine et de ce que son Docker a montré.
type Report struct {
	Results []Result `json:"results,omitempty"`
}

func (r Report) IsEmpty() bool {
	return len(r.Results) == 0
}
