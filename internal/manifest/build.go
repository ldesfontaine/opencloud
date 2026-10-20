package manifest

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Current est l'annuaire tel que la base le montre, lu d'un coup : ce que le
// fichier est comparé à.
type Current struct {
	// Machines donne le nom de chaque machine, par identifiant.
	Machines map[string]string
	// LocalMachineID est la machine openCloud, celle d'une sonde sans
	// « runner ».
	LocalMachineID string
	Projects       []directory.Project
	Services       []directory.Service
	Probes         []probe.Probe
	Heartbeats     []heartbeat.Heartbeat
	Ignored        []directory.IgnoredKey
}

// step est un changement prêt à s'écrire : ce qu'on en affiche, et de quoi
// le faire. Les identifiants des objets que le plan crée ne sont pas connus
// avant l'application : les références restent des noms, résolus à l'écriture.
type step struct {
	change    Change
	project   projectStep
	service   serviceStep
	probe     probeStep
	heartbeat heartbeatStep
	ignored   ignoredStep
}

type projectStep struct {
	Definition directory.ProjectDefinition
	// Before est la définition que le plan a vue : l'application la relit
	// avant d'écrire, et s'arrête si l'objet a bougé entre-temps.
	Before directory.ProjectDefinition
}

type serviceStep struct {
	MachineID string
	// Wanted a pour ProjectIDs des identifiants réels ou en attente
	// (pendingPrefix) ; Projects en garde les noms, que l'application résout
	// sur sa table au moment d'écrire.
	Wanted   directory.ServiceDefinition
	Projects []string
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before directory.ServiceDefinition
}

type probeStep struct {
	Wanted probe.Definition
	// ServiceRef est « machine/nom » ; vide, aucun service.
	ServiceRef string
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before probe.Definition
}

type heartbeatStep struct {
	Wanted heartbeat.Definition
	// Before est la définition que le plan a vue, pour une modification ou
	// une suppression.
	Before heartbeat.Definition
}

type ignoredStep struct {
	Key directory.IgnoredKey
}

// builder construit un plan. Il ne lit rien et n'écrit rien : le fichier, la
// base lue, et des règles.
type builder struct {
	file    File
	current Current
	prune   bool

	problems  []Problem
	unchanged int
	unmanaged []Ref

	// machineIDs donne l'identifiant d'une machine par son nom.
	machineIDs map[string]string
	names      labels
	// Les tables de l'état visé : le nom que le fichier peut citer, et
	// l'identifiant de l'objet, réel ou en attente.
	projectTargets map[string]string
	serviceTargets map[string]string

	// Les changements de chaque genre. Projets et services tiennent des clés
	// d'unicité : leur ordre d'écriture se règle à part, voir orderKeyed.
	projectUpdates, projectCreates []keyedStep
	serviceUpdates, serviceCreates []keyedStep
	projectSteps, serviceSteps     []step
	projectDeletes, serviceDeletes []step
	probeDeletes, probeUpdates     []step
	probeCreates                   []step
	heartbeatDeletes               []step
	heartbeatUpdates               []step
	heartbeatCreates               []step
	ignoredCreates, ignoredDeletes []step
	// Les clés tenues par ce que --prune supprime.
	prunedProjectKeys, prunedServiceKeys map[string]bool
}

// BuildPlan compare le fichier à l'annuaire. C'est une fonction pure : le
// même fichier face à la même base donne le même plan, octet pour octet. Les
// refus de lecture (parsed) arrêtent tout : il n'y a pas de plan d'un fichier
// qu'on ne sait pas lire.
func BuildPlan(file File, parsed []Problem, current Current, prune bool) Plan {
	b := newBuilder(file, current, prune)
	if len(parsed) > 0 {
		b.problems = slices.Clone(parsed)
		return b.finish()
	}
	b.checkMachines()
	b.planProjects()
	b.planServices()
	b.planProbes()
	b.planHeartbeats()
	b.planIgnored()
	return b.finish()
}

func newBuilder(file File, current Current, prune bool) *builder {
	b := &builder{
		file: file, current: current, prune: prune,
		machineIDs:     make(map[string]string, len(current.Machines)),
		projectTargets: map[string]string{},
		serviceTargets: map[string]string{},
		names: labels{
			machines: current.Machines,
			projects: map[string]string{},
			services: map[string]string{},
		},
		prunedProjectKeys: map[string]bool{},
		prunedServiceKeys: map[string]bool{},
	}
	for id, name := range current.Machines {
		b.machineIDs[name] = id
	}
	return b
}

// refuse note un refus à une ligne du fichier. path nomme l'entrée visée.
func (b *builder) refuse(key string, line int, path, value string) {
	if len(b.problems) >= maxProblems {
		return
	}
	b.problems = append(b.problems, Problem{Key: key, Line: line, Path: path, Value: value})
}

// refuseError note le refus d'un composant du domaine, sous sa clé. Une
// erreur sans clé devient un refus générique : jamais le texte de l'erreur,
// qui a pu citer ce que le fichier contient.
func (b *builder) refuseError(err error, line int, path string) {
	if key, refused := refusal.KeyOf(err); refused {
		b.refuse(key, line, path, "")
		return
	}
	b.refuse(KeyEntryInvalid, line, path, path)
}

// checkMachines lit la section « machines » : chaque nom doit être celui d'une
// machine connue, une fois. Le fichier ne crée jamais une machine.
func (b *builder) checkMachines() {
	seen := map[string]bool{}
	for _, entry := range b.file.Machines {
		switch {
		case entry.Name == "":
			b.refuse(KeyEntryInvalid, entry.Line, "machines", "machines")
		case seen[entry.Name]:
			b.refuse(KeyNameDuplicate, entry.Line, entry.Name, entry.Name)
		default:
			if _, known := b.machineIDs[entry.Name]; !known {
				b.refuse(KeyMachineUnknown, entry.Line, entry.Name, entry.Name)
			}
		}
		seen[entry.Name] = true
	}
}

// machineID résout un nom de machine du fichier. Un nom inconnu est un refus
// du plan, à la ligne de l'entrée.
func (b *builder) machineID(name string, line int, path string) (string, bool) {
	id, known := b.machineIDs[name]
	if !known {
		b.refuse(KeyMachineUnknown, line, path, name)
	}
	return id, known
}

// afterOnly donne à un champ créé sa seule valeur d'après : il n'avait pas
// d'avant.
func afterOnly(fields []FieldChange) []FieldChange {
	for index := range fields {
		fields[index].Before = ""
	}
	return fields
}

// countedCarries ne garde que ce qui emporte vraiment quelque chose.
func countedCarries(carries ...Carry) []Carry {
	kept := []Carry{}
	for _, carry := range carries {
		if carry.Count > 0 {
			kept = append(kept, carry)
		}
	}
	return kept
}

// finish range les changements dans l'ordre d'écriture, qui est celui de
// l'affichage, et scelle le plan.
func (b *builder) finish() Plan {
	plan := Plan{Prune: b.prune, Unchanged: b.unchanged}
	slices.SortStableFunc(b.problems, func(a, c Problem) int {
		return cmp.Or(cmp.Compare(boolRank(a.Line == 0), boolRank(c.Line == 0)), cmp.Compare(a.Line, c.Line))
	})
	plan.Problems = b.problems
	if plan.Problems == nil {
		plan.Problems = []Problem{}
	}
	ordered := [][]step{
		b.projectSteps, b.serviceSteps,
		sortedByName(b.probeDeletes), b.probeUpdates, b.probeCreates,
		sortedByName(b.heartbeatDeletes), b.heartbeatUpdates, b.heartbeatCreates,
		b.ignoredCreates, sortedByName(b.ignoredDeletes),
		sortedByName(b.serviceDeletes), sortedByName(b.projectDeletes),
	}
	for _, steps := range ordered {
		plan.steps = append(plan.steps, steps...)
	}
	plan.Changes = make([]Change, 0, len(plan.steps))
	for _, step := range plan.steps {
		plan.Changes = append(plan.Changes, step.change)
	}
	slices.SortFunc(b.unmanaged, func(a, c Ref) int {
		return cmp.Or(cmp.Compare(kindRank(a.Kind), kindRank(c.Kind)), cmp.Compare(a.Name, c.Name), cmp.Compare(a.ID, c.ID))
	})
	plan.Unmanaged = b.unmanaged
	if plan.Unmanaged == nil {
		plan.Unmanaged = []Ref{}
	}
	plan.projects, plan.services = b.projectTargets, b.serviceTargets
	plan.Hash = hashChanges(plan.Prune, plan.Changes)
	return plan
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

func kindRank(kind Kind) int {
	return slices.Index(Kinds(), kind)
}

// sortedByName range des suppressions par nom puis identifiant : l'ordre des
// lignes de la base ne doit pas changer l'empreinte.
func sortedByName(steps []step) []step {
	sorted := slices.Clone(steps)
	slices.SortStableFunc(sorted, func(a, c step) int {
		return cmp.Or(cmp.Compare(a.change.Name, c.change.Name), cmp.Compare(a.change.ID, c.change.ID))
	})
	return sorted
}

// String décrit un changement pour un journal technique, sans valeur.
func (c Change) String() string {
	return fmt.Sprintf("%s %s", c.Action, c.Label())
}
