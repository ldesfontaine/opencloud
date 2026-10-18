package directory

import (
	"cmp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// GapState est l'écart entre ce qui est déclaré et ce que la machine
// montre. Il ne se stocke pas : il se calcule à chaque lecture.
type GapState string

const (
	// Ce que la machine montre est ce que la déclaration dit.
	GapConform GapState = "conform"
	// Déclaré, mais aucun conteneur vivant n'y est rattaché.
	GapAbsent GapState = "absent"
	// Un conteneur tourne autre chose que ce qui est déclaré.
	GapDiverged GapState = "diverged"
	// Rien ne permet de dire si la version déclarée est celle qui tourne.
	GapUnverifiable GapState = "unverifiable"
	// Un binaire : aucun agent ne le regarde encore.
	GapUnobserved GapState = "unobserved"
)

func GapStates() []GapState {
	return []GapState{GapConform, GapAbsent, GapDiverged, GapUnverifiable, GapUnobserved}
}

// GapReason dit pourquoi un conteneur n'est pas conforme.
type GapReason string

const (
	ReasonNone            GapReason = ""
	ReasonImageDiffers    GapReason = "image_differs"
	ReasonDigestDiffers   GapReason = "digest_differs"
	ReasonTagLatest       GapReason = "tag_latest"
	ReasonVersionUnpinned GapReason = "version_unpinned"
	ReasonTagDiffers      GapReason = "tag_differs"
	ReasonDigestUnknown   GapReason = "digest_unknown"
	ReasonImageUnreadable GapReason = "image_unreadable"
)

// GapReasons liste les motifs ; le vide, conforme, n'en est pas un.
func GapReasons() []GapReason {
	return []GapReason{
		ReasonImageDiffers, ReasonDigestDiffers, ReasonTagLatest, ReasonVersionUnpinned,
		ReasonTagDiffers, ReasonDigestUnknown, ReasonImageUnreadable,
	}
}

// ContainerVerdict est ce que dit un conteneur rattaché, face à son unité.
type ContainerVerdict struct {
	// ServiceID est l'identifiant de la fiche du conteneur, pas celui d'un
	// service déclaré.
	ServiceID string
	Name      string
	State     container.State
	Image     string
	Level     container.MatchLevel
	Verdict   GapState
	Reason    GapReason
}

type UnitGap struct {
	UnitID string
	Name   string
	Kind   UnitKind
	State  GapState
	// Faible : l'un des conteneurs n'est rattaché que par son nom.
	Weak       bool
	Containers []ContainerVerdict
}

type ServiceGap struct {
	ServiceID string
	State     GapState
	Weak      bool
	Units     []UnitGap
}

// Undeclared est ce que la machine montre sans que rien ne le déclare,
// regroupé par la clé que l'opérateur peut ignorer.
type Undeclared struct {
	MachineID string
	Key       IgnoredKey
	// Le service déclaré du même projet Compose sur la machine, s'il existe :
	// c'est là que « Adopter » ajoute une unité.
	ServiceID  string
	Ignored    bool
	Containers []container.Container
	// Les conteneurs du groupe dont les labels ne visent aucune unité de leur
	// machine, par identifiant de fiche, avec le motif. Un label orphelin
	// laisse le conteneur non déclaré : rien ne se replie sur Compose ni sur
	// le nom.
	LabelProblems map[string]LabelProblem
	// Adoptable : au moins un conteneur du groupe n'a pas de label. Adopter
	// un conteneur étiqueté créerait une unité que son label empêcherait de
	// rattacher ; un groupe d'orphelins se corrige par le label.
	Adoptable bool
}

// GapReport porte les services déclarés qu'il a jugés, dans le même ordre
// que Services : un lecteur qui veut les deux ne relit pas l'annuaire, et
// ne risque pas d'apparier un service au mauvais écart.
type GapReport struct {
	Declared   []Service
	Services   []ServiceGap
	Undeclared []Undeclared
}

// Gaps calcule l'écart. Il lit le rattachement que les fiches portent, sans
// relancer le rapprochement : ce que l'écran montre est ce que la base
// garde. Les conteneurs archivés ne comptent pas.
func Gaps(declared []Service, containers []container.Container, checks map[string]update.Check, ignored []IgnoredKey) GapReport {
	living := make([]container.Container, 0, len(containers))
	for _, item := range containers {
		if !item.IsArchived() {
			living = append(living, item)
		}
	}
	attached := map[string][]container.Container{}
	for _, item := range living {
		if item.UnitID != "" {
			attached[item.UnitID] = append(attached[item.UnitID], item)
		}
	}
	report := GapReport{Declared: declared, Services: make([]ServiceGap, 0, len(declared))}
	for _, declaredService := range declared {
		report.Services = append(report.Services, gapOfService(declaredService, attached, checks))
	}
	report.Undeclared = undeclaredOf(declared, living, ignored)
	return report
}

func gapOfService(declared Service, attached map[string][]container.Container, checks map[string]update.Check) ServiceGap {
	gap := ServiceGap{ServiceID: declared.ID, State: GapConform, Units: make([]UnitGap, 0, len(declared.Units))}
	for _, unit := range declared.Units {
		unitGap := gapOfUnit(unit, attached[unit.ID], checks)
		gap.Units = append(gap.Units, unitGap)
		gap.State = worstOfService(gap.State, unitGap.State)
		gap.Weak = gap.Weak || unitGap.Weak
	}
	return gap
}

func gapOfUnit(unit Unit, containers []container.Container, checks map[string]update.Check) UnitGap {
	gap := UnitGap{UnitID: unit.ID, Name: unit.Name, Kind: unit.Kind, Containers: []ContainerVerdict{}}
	if unit.Kind == UnitBinary {
		gap.State = GapUnobserved
		return gap
	}
	if len(containers) == 0 {
		gap.State = GapAbsent
		return gap
	}
	gap.State = GapConform
	for _, item := range sortedByName(containers) {
		state, reason := verdict(unit, item, checks)
		gap.Containers = append(gap.Containers, ContainerVerdict{
			ServiceID: item.ID, Name: item.Name, State: item.State, Image: item.Image,
			Level: item.MatchLevel, Verdict: state, Reason: reason,
		})
		gap.State = worstOfContainers(gap.State, state)
		gap.Weak = gap.Weak || item.MatchLevel.IsWeak()
	}
	return gap
}

// verdict juge un conteneur rattaché face à son unité. La première règle
// qui s'applique décide ; l'ordre est celui du tableau de 22-fonctionnement.md.
func verdict(unit Unit, item container.Container, checks map[string]update.Check) (GapState, GapReason) {
	observed, err := registry.Parse(item.Image)
	if err != nil {
		return GapUnverifiable, ReasonImageUnreadable
	}
	declared, err := registry.Parse(unit.Image)
	if err != nil {
		// Une image déclarée que le registre ne saurait pas nommer ne se
		// compare à rien. La validation ne l'admet plus, mais des lignes
		// plus anciennes peuvent en porter.
		return GapUnverifiable, ReasonImageUnreadable
	}
	if observed.Registry != declared.Registry || observed.Repository != declared.Repository {
		return GapDiverged, ReasonImageDiffers
	}
	digest := observedDigest(observed, item, checks)
	if unit.Digest != "" && digest != "" && digest != unit.Digest {
		return GapDiverged, ReasonDigestDiffers
	}
	// Une image épinglée par son empreinte dit exactement ce qui tourne, tag
	// ou non. Seule l'empreinte que l'image écrit vaut ici : celle que le
	// dernier constat a lue localement ne dit pas ce que le conteneur a
	// démarré, et un tag mobile peut avoir bougé depuis.
	if unit.Digest != "" && observed.Digest == unit.Digest {
		return GapConform, ReasonNone
	}
	if observed.Tag == "" || strings.EqualFold(observed.Tag, registry.DefaultTag) {
		return GapUnverifiable, ReasonTagLatest
	}
	if unit.Version == "" {
		return GapUnverifiable, ReasonVersionUnpinned
	}
	if observed.Tag != unit.Version {
		return GapDiverged, ReasonTagDiffers
	}
	if unit.Digest != "" && digest == "" {
		return GapUnverifiable, ReasonDigestUnknown
	}
	return GapConform, ReasonNone
}

// observedDigest est l'empreinte de ce qui tourne : celle que l'image écrit,
// sinon celle que le dernier constat sur l'image a lue localement. Vide : on
// ne la connaît pas.
func observedDigest(observed registry.Reference, item container.Container, checks map[string]update.Check) string {
	if observed.Digest != "" {
		return observed.Digest
	}
	if check, ok := update.CheckFor(checks, item); ok {
		return check.LocalDigest
	}
	return ""
}

func worstOfContainers(current, next GapState) GapState {
	return worst(current, next, []GapState{GapConform, GapUnverifiable, GapDiverged})
}

func worstOfService(current, next GapState) GapState {
	return worst(current, next, []GapState{GapConform, GapUnobserved, GapUnverifiable, GapDiverged, GapAbsent})
}

// worst rend le plus grave des deux, l'échelle allant du moins grave au
// plus grave.
func worst(current, next GapState, scale []GapState) GapState {
	if slices.Index(scale, next) > slices.Index(scale, current) {
		return next
	}
	return current
}

func sortedByName(containers []container.Container) []container.Container {
	sorted := slices.Clone(containers)
	slices.SortFunc(sorted, func(a, b container.Container) int {
		if byName := strings.Compare(a.Name, b.Name); byName != 0 {
			return byName
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sorted
}

// keyOf est la clé par laquelle l'opérateur ignore un conteneur : son projet
// Compose, sinon son nom.
func keyOf(item container.Container) IgnoredKey {
	if item.Group != "" {
		return IgnoredKey{MachineID: item.MachineID, Kind: IgnoreComposeProject, Value: item.Group}
	}
	return IgnoredKey{MachineID: item.MachineID, Kind: IgnoreContainerName, Value: item.Name}
}

func undeclaredOf(declared []Service, living []container.Container, ignored []IgnoredKey) []Undeclared {
	groups := map[IgnoredKey]*Undeclared{}
	for _, item := range living {
		if item.UnitID != "" {
			continue
		}
		key := keyOf(item)
		group, found := groups[key]
		if !found {
			group = &Undeclared{MachineID: item.MachineID, Key: key, ServiceID: composeServiceOf(declared, key), LabelProblems: map[string]LabelProblem{}}
			groups[key] = group
		}
		group.Containers = append(group.Containers, item)
		if problem := LabelProblemOf(declared, item); problem != LabelOK {
			group.LabelProblems[item.ID] = problem
		}
		group.Adoptable = group.Adoptable || item.Labels.IsEmpty()
	}
	undeclared := make([]Undeclared, 0, len(groups))
	for _, group := range groups {
		group.Containers = sortedByName(group.Containers)
		group.Ignored = isIgnored(ignored, group.Key)
		undeclared = append(undeclared, *group)
	}
	slices.SortFunc(undeclared, func(a, b Undeclared) int {
		return cmp.Or(
			strings.Compare(a.MachineID, b.MachineID),
			strings.Compare(string(a.Key.Kind), string(b.Key.Kind)),
			strings.Compare(a.Key.Value, b.Key.Value),
		)
	})
	return undeclared
}

// composeServiceOf rend le service déclaré dont le projet Compose est celui
// de la clé, sur la même machine.
func composeServiceOf(declared []Service, key IgnoredKey) string {
	if key.Kind != IgnoreComposeProject {
		return ""
	}
	for _, candidate := range declared {
		if candidate.Origin == OriginCompose && candidate.MachineID == key.MachineID && candidate.ComposeProject == key.Value {
			return candidate.ID
		}
	}
	return ""
}

func isIgnored(ignored []IgnoredKey, key IgnoredKey) bool {
	return slices.ContainsFunc(ignored, func(candidate IgnoredKey) bool {
		return candidate.MachineID == key.MachineID && candidate.Kind == key.Kind && candidate.Value == key.Value
	})
}
