package alert

import (
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Resource est ce qu'un seuil juge : une des trois mesures d'une machine.
type Resource string

const (
	ResourceCPU    Resource = "cpu"
	ResourceMemory Resource = "memory"
	ResourceDisk   Resource = "disk"
)

// Resources liste les ressources dans l'ordre des écrans.
func Resources() []Resource {
	return []Resource{ResourceCPU, ResourceMemory, ResourceDisk}
}

func isResource(resource Resource) bool {
	return slices.Contains(Resources(), resource)
}

// Le retour se tient cinq points sous l'attention : une ressource qui
// oscille autour du seuil ne fait pas clignoter l'alerte.
const RecoveryGap = 5

// La clé est celle du réglage, que l'opérateur corrige dans les paramètres
// ou sur la fiche de la machine.
var ErrThresholdInvalid = refusal.Invalid("settings.threshold_invalid", "alert: threshold invalid")

// Rule est un seuil de ressource : au-dessus de Attention l'alerte s'ouvre,
// au-dessus de Danger elle s'aggrave, et elle ne se résout que sous
// Recovery. Window dit sur quoi on juge : zéro, la dernière lecture (le
// disque, qui ne bouge pas par à-coups) ; sinon la moyenne de la fenêtre, pour
// qu'un pic d'une minute ne réveille personne.
type Rule struct {
	Resource  Resource
	Attention int
	Danger    int
	Window    time.Duration
}

func (r Rule) Recovery() int {
	return r.Attention - RecoveryGap
}

func (r Rule) IsWindowed() bool {
	return r.Window > 0
}

// Bounds sont les bornes d'une règle, par ressource : le formulaire les dit
// sans les recopier. Sous 50 %, une ressource ordinaire alerte en
// permanence ; un volume à 100 % est déjà trop tard, une moyenne de
// processeur ou de mémoire peut s'y tenir.
type Bounds struct {
	MinAttention int
	MaxDanger    int
	// MinWindow et MaxWindow : zéro pour une ressource jugée sur la dernière
	// lecture.
	MinWindow time.Duration
	MaxWindow time.Duration
}

func (b Bounds) IsWindowed() bool {
	return b.MaxWindow > 0
}

func BoundsOf(resource Resource) Bounds {
	if resource == ResourceDisk {
		return Bounds{MinAttention: 50, MaxDanger: 99}
	}
	return Bounds{MinAttention: 50, MaxDanger: 100, MinWindow: time.Minute, MaxWindow: 2 * time.Hour}
}

// DefaultRule est la règle du produit tant que l'opérateur n'a rien réglé.
func DefaultRule(resource Resource) Rule {
	switch resource {
	case ResourceCPU:
		return Rule{Resource: resource, Attention: 95, Danger: 99, Window: 15 * time.Minute}
	case ResourceMemory:
		return Rule{Resource: resource, Attention: 90, Danger: 95, Window: 15 * time.Minute}
	}
	return Rule{Resource: ResourceDisk, Attention: 85, Danger: 95}
}

// Validate refuse une règle hors de ses bornes : ressource inconnue,
// attention qui ne reste pas sous le danger, fenêtre là où la ressource n'en
// a pas ou hors de ses bornes.
func (r Rule) Validate() error {
	if !isResource(r.Resource) {
		return ErrThresholdInvalid
	}
	bounds := BoundsOf(r.Resource)
	if r.Attention < bounds.MinAttention || r.Attention >= r.Danger || r.Danger > bounds.MaxDanger {
		return ErrThresholdInvalid
	}
	if !bounds.IsWindowed() {
		if r.Window != 0 {
			return ErrThresholdInvalid
		}
		return nil
	}
	if r.Window < bounds.MinWindow || r.Window > bounds.MaxWindow || r.Window%time.Minute != 0 {
		return ErrThresholdInvalid
	}
	return nil
}

// Origin dit d'où vient la règle qui s'applique à une machine.
type Origin string

const (
	// Le réglage global, sous Paramètres.
	OriginGlobal Origin = "global"
	// La surcharge posée sur la fiche de la machine.
	OriginMachine Origin = "machine"
	// Rien n'est réglé : les valeurs du produit.
	OriginDefault Origin = "default"
)

// Applied est la règle en vigueur pour une ressource, et d'où elle vient :
// l'écran dit lequel s'applique.
type Applied struct {
	Rule
	Origin Origin
}

// Threshold est une règle réglée en base : MachineID vide, c'est le
// réglage global.
type Threshold struct {
	MachineID string
	Rule
}

// resolve rend, pour chaque ressource et dans l'ordre de Resources, la
// règle qui s'applique : la surcharge de la machine, sinon le réglage
// global, sinon la valeur du produit. Une règle réglée hors des bornes (écrite à la main en base)
// est ignorée au profit de la suivante : une valeur fausse ne coupe jamais l'alerte.
func resolve(rows []Threshold, machineID string) []Applied {
	applied := make([]Applied, 0, len(Resources()))
	for _, resource := range Resources() {
		applied = append(applied, resolveOne(rows, machineID, resource))
	}
	return applied
}

func resolveOne(rows []Threshold, machineID string, resource Resource) Applied {
	if machineID != "" {
		if rule, ok := findRule(rows, machineID, resource); ok {
			return Applied{Rule: rule, Origin: OriginMachine}
		}
	}
	if rule, ok := findRule(rows, "", resource); ok {
		return Applied{Rule: rule, Origin: OriginGlobal}
	}
	return Applied{Rule: DefaultRule(resource), Origin: OriginDefault}
}

func findRule(rows []Threshold, machineID string, resource Resource) (Rule, bool) {
	for _, row := range rows {
		if row.MachineID == machineID && row.Resource == resource && row.Rule.Validate() == nil {
			return row.Rule, true
		}
	}
	return Rule{}, false
}
