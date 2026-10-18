package directory

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// ErrLinkUnplanned : la reprise de l'existant n'a pas su dire à quelle unité
// rattacher un conteneur qui porte un lien (une sonde, un membre de composant,
// une politique). Elle s'arrête plutôt que de le perdre.
var ErrLinkUnplanned = errors.New("a linked container has no unit in the plan")

// LinkPlan est ce que la reprise de l'existant décide, sans rien écrire : les
// services à créer, les unités à ajouter à des services de l'opérateur, ces
// services à marquer « à vérifier », et pour chaque fiche liée l'unité où elle
// va.
type LinkPlan struct {
	// Provenance migration, à vérifier, nature application.
	Created []Service
	// Unités ajoutées à un service qui existait déjà (ServiceID posé).
	Added []Unit
	// Les services existants qui ont reçu une unité : l'opérateur les relit.
	Reviewed []string
	Links    []LinkedFiche
}

// LinkedFiche dit où va une fiche de conteneur : son unité, le service qui la
// porte, et par quoi elle s'y rattache.
type LinkedFiche struct {
	ContainerID string
	UnitID      string
	ServiceID   string
	Level       container.MatchLevel
}

// PlanLinks décide où vont les conteneurs qui portent un lien. Les fiches
// sont toutes celles de la base, archivées comprises ; linked nomme celles
// que quelque chose vise (sonde, membre de composant, politique de mise à
// jour) ; declared est l'annuaire tel qu'il est. La fonction est pure :
// newID tire les identifiants, et le résultat ne dépend ni de l'ordre des
// fiches ni de celui des maps.
//
// Les règles, dans l'ordre :
//  1. Une fiche déjà rattachée garde son unité.
//  2. Sinon, un service déclaré qui correspond (la règle du rapprochement,
//     sans le filtre « vivant », le projet avant le nom) la reçoit. Le projet
//     Compose déclaré qui n'a pas l'unité du service Compose la gagne, et son
//     service passe « à vérifier ».
//  3. Sinon, un service est créé par dossier, une seule fois quel que soit le
//     nombre de fiches liées : le projet Compose (une unité par service
//     Compose lié ou qui tourne, jamais pour un conteneur arrêté que rien ne
//     vise), ou le conteneur seul. Un projet que l'opérateur a déjà déclaré
//     n'est jamais recréé : ce qui n'y tient pas descend en conteneur seul.
//  4. Toutes les fiches de la clé reçoivent leur unité, vivantes et
//     archivées : elles disent « était rattaché à ». Les fiches des services
//     Compose que le plan n'a pas retenus restent non déclarées.
//  5. Une fiche liée sans unité fait échouer le plan : jamais de lien perdu
//     en silence.
func PlanLinks(fiches []container.Container, linked map[string]bool, declared []Service, newID func(prefix string) (string, error)) (LinkPlan, error) {
	planner := newLinkPlanner(linked, declared, newID)
	if err := planner.plan(sortedForPlan(fiches)); err != nil {
		return LinkPlan{}, err
	}
	return planner.finish()
}

type linkPlanner struct {
	linked map[string]bool
	newID  func(prefix string) (string, error)

	// known : les services déclarés puis ceux que le plan crée, pour que les
	// noms libres tiennent compte des deux.
	known         []Service
	index         unitIndex
	serviceOfUnit map[string]string
	// Le nom de conteneur (machine comprise) que chaque unité déclare, de
	// toute origine : l'annuaire n'en admet pas deux, et aucun index unique
	// ne le garde.
	containerNames map[string]string
	// Le service Compose déclaré d'un projet d'une machine, par composeKey
	// sans service Compose.
	projects map[string]string
	// Le nombre d'unités de chaque service, ajouts compris.
	unitCount map[string]int

	result LinkPlan
	mapped map[string]bool
	// La première erreur tirée d'un identifiant : le plan ne se lit pas sans.
	idError error
}

func newLinkPlanner(linked map[string]bool, declared []Service, newID func(prefix string) (string, error)) *linkPlanner {
	planner := &linkPlanner{
		linked: linked, newID: newID, known: slices.Clone(declared),
		index:         indexUnits(declared),
		serviceOfUnit: map[string]string{}, containerNames: map[string]string{}, projects: map[string]string{}, unitCount: map[string]int{},
		mapped: map[string]bool{},
	}
	for _, existing := range declared {
		planner.unitCount[existing.ID] = len(existing.Units)
		for _, unit := range existing.Units {
			planner.serviceOfUnit[unit.ID] = existing.ID
			if unit.ContainerName != "" {
				planner.containerNames[nameKey(existing.MachineID, unit.ContainerName)] = unit.ID
			}
		}
		if existing.Origin == OriginCompose {
			planner.projects[composeKey(existing.MachineID, existing.ComposeProject, "")] = existing.ID
		}
	}
	return planner
}

// sortedForPlan met les fiches dans un ordre qui ne dépend de rien d'autre
// que de leur contenu.
func sortedForPlan(fiches []container.Container) []container.Container {
	sorted := slices.Clone(fiches)
	slices.SortFunc(sorted, func(a, b container.Container) int {
		return cmp.Or(
			strings.Compare(a.MachineID, b.MachineID),
			strings.Compare(a.Group, b.Group),
			strings.Compare(a.ComposeService, b.ComposeService),
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.ID, b.ID),
		)
	})
	return sorted
}

func (p *linkPlanner) plan(fiches []container.Container) error {
	unresolved, err := p.resolveDeclared(fiches)
	if err != nil {
		return err
	}
	unresolved = p.addToDeclaredProjects(unresolved)
	unresolved = p.createComposeServices(unresolved)
	p.createContainerServices(unresolved)
	return nil
}

// resolveDeclared rattache ce que l'annuaire déclaré rattache déjà : la
// fiche qui a une unité la garde, une autre est cherchée par la règle du
// rapprochement. Il rend les fiches que rien ne reçoit.
func (p *linkPlanner) resolveDeclared(fiches []container.Container) ([]container.Container, error) {
	var unresolved []container.Container
	for _, fiche := range fiches {
		if fiche.UnitID != "" {
			// Elle a déjà son unité : il n'y a rien à écrire, sauf pour une
			// fiche liée, dont le lien doit être repointé.
			if p.linked[fiche.ID] {
				if err := p.linkToDeclared(fiche, fiche.UnitID, fiche.MatchLevel); err != nil {
					return nil, err
				}
			}
			continue
		}
		if match, ok := matchOne(p.index, fiche); ok {
			if err := p.linkToDeclared(fiche, match.UnitID, match.Level); err != nil {
				return nil, err
			}
			continue
		}
		unresolved = append(unresolved, fiche)
	}
	return unresolved, nil
}

// linkToDeclared note où va une fiche qui a trouvé son unité dans l'annuaire.
// Toutes celles de la clé sont notées, liées ou non : une fiche archivée n'est
// jamais recalculée par l'inventaire, et elle doit dire « était rattaché à »
// comme celles des services que le plan crée. Une fiche que rien ne vise ne
// fait pas échouer le plan si son unité a disparu.
func (p *linkPlanner) linkToDeclared(fiche container.Container, unitID string, level container.MatchLevel) error {
	serviceID, ok := p.serviceOfUnit[unitID]
	if !ok {
		if !p.linked[fiche.ID] {
			return nil
		}
		return fmt.Errorf("%w: %s is attached to unit %s, which the directory does not hold", ErrLinkUnplanned, fiche.ID, unitID)
	}
	p.link(fiche.ID, unitID, serviceID, level)
	return nil
}

func (p *linkPlanner) link(containerID, unitID, serviceID string, level container.MatchLevel) {
	p.result.Links = append(p.result.Links, LinkedFiche{ContainerID: containerID, UnitID: unitID, ServiceID: serviceID, Level: level})
	p.mapped[containerID] = true
}

// isComposeFiche dit si la fiche se retrouve par son projet et son service
// Compose, et si l'annuaire saurait les écrire : le projet doit être un nom de
// projet Compose valable et le service un nom d'unité, la règle du
// rapprochement les comparant tels quels. Une fiche dont le label sort de ces
// formes descend en conteneur seul plutôt que de faire échouer le démarrage
// sur un service que l'opérateur ne pourrait pas enregistrer.
func isComposeFiche(fiche container.Container) bool {
	return namePattern.MatchString(fiche.Group) && unitNamePattern.MatchString(fiche.ComposeService)
}

// addToDeclaredProjects donne l'unité du service Compose aux fiches liées
// d'un projet que l'opérateur a déjà déclaré sans elle, et marque ce service
// « à vérifier ». Le service plein ne reçoit rien : ses fiches liées
// descendent en conteneurs seuls.
func (p *linkPlanner) addToDeclaredProjects(unresolved []container.Container) []container.Container {
	var remaining []container.Container
	added := map[string]string{}
	for _, group := range groupFiches(unresolved, composeFicheKey) {
		serviceID, declaredProject := p.projects[composeKey(group[0].MachineID, group[0].Group, "")]
		first := group[0]
		if !isComposeFiche(first) || !declaredProject || !p.anyLinked(group) {
			remaining = append(remaining, group...)
			continue
		}
		unitKey := composeKey(first.MachineID, first.Group, first.ComposeService)
		unitID, done := added[unitKey]
		if !done {
			if p.unitCount[serviceID] >= MaxUnits {
				remaining = append(remaining, group...)
				continue
			}
			unit := p.newUnit(serviceID, unitOfCompose(latest(group)))
			p.result.Added = append(p.result.Added, unit)
			p.result.Reviewed = append(p.result.Reviewed, serviceID)
			p.unitCount[serviceID]++
			unitID = unit.ID
			added[unitKey] = unitID
		}
		for _, fiche := range group {
			p.link(fiche.ID, unitID, serviceID, container.MatchCompose)
		}
	}
	return remaining
}

// createComposeServices crée un service par projet Compose que rien ne
// déclare, avec une unité par service Compose : ceux des fiches liées
// d'abord, puis les autres fiches vivantes, jusqu'à MaxUnits. Les fiches liées
// qui ne tiennent pas dans le service restent à placer.
func (p *linkPlanner) createComposeServices(unresolved []container.Container) []container.Container {
	var remaining, composeFiches []container.Container
	for _, fiche := range unresolved {
		if isComposeFiche(fiche) {
			composeFiches = append(composeFiches, fiche)
		} else {
			remaining = append(remaining, fiche)
		}
	}
	for _, project := range groupFiches(composeFiches, projectKey) {
		// Un projet que l'opérateur a déclaré a son service : en créer un
		// second violerait l'unicité (machine, projet). Ce qui n'a pas tenu
		// dans le service déclaré descend en conteneur seul.
		if _, declared := p.projects[projectKey(project[0])]; declared || !p.anyLinked(project) {
			remaining = append(remaining, project...)
			continue
		}
		remaining = append(remaining, p.createComposeService(project)...)
	}
	return remaining
}

// createComposeService crée le service du projet et rend les fiches qu'il
// n'a pas reçues.
func (p *linkPlanner) createComposeService(project []container.Container) []container.Container {
	first := project[0]
	groups := groupFiches(project, composeFicheKey)
	chosen := chooseComposeServices(groups, p.linked)
	created := p.newService(first.MachineID, slug(first.Group), OriginCompose)
	created.ComposeProject = first.Group
	var remaining []container.Container
	for _, group := range groups {
		if !slices.Contains(chosen, group[0].ComposeService) {
			remaining = append(remaining, group...)
		}
	}
	for _, composeService := range chosen {
		group := groups[slices.IndexFunc(groups, func(candidate []container.Container) bool { return candidate[0].ComposeService == composeService })]
		unit := p.newUnit(created.ID, unitOfCompose(latest(group)))
		created.Units = append(created.Units, unit)
		for _, fiche := range group {
			p.link(fiche.ID, unit.ID, created.ID, container.MatchCompose)
		}
	}
	p.addCreated(created)
	return remaining
}

// chooseComposeServices garde au plus MaxUnits services Compose du projet :
// ceux qu'un lien vise d'abord, puis ceux qui ont une fiche qui tourne
// (running ou restarting). Un conteneur arrêté que rien ne vise, comme
// l'`init` d'un projet qui s'est terminé en 0, ne devient pas une unité : une
// unité sans conteneur qui tourne ferait passer le composant de statut en
// panne sans que rien n'ait changé. Il reste non déclaré, adoptable. Le
// résultat est trié par nom.
func chooseComposeServices(groups [][]container.Container, linked map[string]bool) []string {
	var withLink, living []string
	for _, group := range groups {
		name := group[0].ComposeService
		switch {
		case slices.ContainsFunc(group, func(fiche container.Container) bool { return linked[fiche.ID] }):
			withLink = append(withLink, name)
		case slices.ContainsFunc(group, runs):
			living = append(living, name)
		}
	}
	chosen := append(withLink, living...)
	if len(chosen) > MaxUnits {
		chosen = chosen[:MaxUnits]
	}
	slices.Sort(chosen)
	return chosen
}

// runs dit si la fiche est un conteneur qui tourne ou qui redémarre.
func runs(fiche container.Container) bool {
	return !fiche.IsArchived() && (fiche.State == container.StateRunning || fiche.State == container.StateRestarting)
}

// createContainerServices crée un service « conteneur seul » par nom de
// conteneur dont une fiche porte un lien, pour tout ce que les projets n'ont
// pas reçu. Une fiche sans lien et sans projet à créer n'est pas reprise.
func (p *linkPlanner) createContainerServices(unresolved []container.Container) {
	for _, group := range groupFiches(unresolved, nameKeyOfFiche) {
		if !p.anyLinked(group) {
			continue
		}
		fiche := latest(group)
		// Un nom de conteneur que le dossier d'un projet déclaré porte déjà :
		// c'est cette unité-là, créer la nôtre ferait deux services pour une
		// même clé d'observation.
		if unitID, taken := p.containerNames[nameKeyOfFiche(fiche)]; taken {
			for _, member := range group {
				p.link(member.ID, unitID, p.serviceOfUnit[unitID], container.MatchName)
			}
			continue
		}
		created := p.newService(fiche.MachineID, slug(fiche.Name), OriginContainer)
		unit := p.newUnit(created.ID, unitOfContainer(fiche))
		created.Units = []Unit{unit}
		p.containerNames[nameKeyOfFiche(fiche)] = unit.ID
		p.serviceOfUnit[unit.ID] = created.ID
		for _, member := range group {
			p.link(member.ID, unit.ID, created.ID, container.MatchName)
		}
		p.addCreated(created)
	}
}

func (p *linkPlanner) anyLinked(group []container.Container) bool {
	return slices.ContainsFunc(group, func(fiche container.Container) bool { return p.linked[fiche.ID] })
}

// newService prépare un service repris de l'existant, avec un nom libre sur
// sa machine. L'identifiant est tiré ici ; une erreur de tirage, qui n'arrive
// pas, se garde pour finish.
func (p *linkPlanner) newService(machineID, base string, origin Origin) Service {
	id := p.mustID(ServicePrefix)
	return Service{
		ID: id, MachineID: machineID, Name: freeName(base, machineID, p.known),
		Nature: NatureApplication, Origin: origin, DeclaredBy: ByMigration, NeedsReview: true,
		Provides: []Capability{}, Units: []Unit{}, ProjectIDs: []string{},
	}
}

func (p *linkPlanner) addCreated(created Service) {
	p.known = append(p.known, created)
	p.result.Created = append(p.result.Created, created)
	p.unitCount[created.ID] = len(created.Units)
}

func (p *linkPlanner) newUnit(serviceID string, definition UnitDefinition) Unit {
	return unitOf(p.mustID(UnitPrefix), serviceID, definition.complete(UnitContainer))
}

// mustID tire un identifiant ; si le tirage échoue, l'erreur est gardée et
// l'identifiant vide, et finish la rend : le plan ne se lit pas sans elle.
func (p *linkPlanner) mustID(prefix string) string {
	id, err := p.newID(prefix)
	if err != nil {
		p.idError = errors.Join(p.idError, err)
	}
	return id
}

func (p *linkPlanner) finish() (LinkPlan, error) {
	if p.idError != nil {
		return LinkPlan{}, fmt.Errorf("generate ids: %w", p.idError)
	}
	for _, id := range slices.Sorted(maps.Keys(p.linked)) {
		if p.linked[id] && !p.mapped[id] {
			return LinkPlan{}, fmt.Errorf("%w: %s", ErrLinkUnplanned, id)
		}
	}
	slices.SortFunc(p.result.Links, func(a, b LinkedFiche) int { return strings.Compare(a.ContainerID, b.ContainerID) })
	p.result.Reviewed = slices.Compact(slices.Sorted(slices.Values(p.result.Reviewed)))
	return p.result, nil
}

// unitOfCompose décrit l'unité d'un service Compose d'après la fiche la plus
// récente de sa clé.
func unitOfCompose(fiche container.Container) UnitDefinition {
	unit := unitProposal(fiche)
	unit.Name = fiche.ComposeService
	return unit
}

// unitOfContainer décrit l'unité d'un conteneur seul : le nom du conteneur
// est la clé par laquelle il se retrouve.
func unitOfContainer(fiche container.Container) UnitDefinition {
	unit := unitProposal(fiche)
	unit.Name = unitNameOf(fiche.Name)
	unit.ContainerName = fiche.Name
	return unit
}

// latest rend la fiche la plus récente d'un groupe : une vivante avant une
// archivée, puis la plus récemment vue, puis l'identifiant le plus petit.
func latest(group []container.Container) container.Container {
	return slices.MinFunc(group, func(a, b container.Container) int {
		return cmp.Or(
			cmp.Compare(boolRank(a.IsArchived()), boolRank(b.IsArchived())),
			b.LastSeenAt.Compare(a.LastSeenAt),
			strings.Compare(a.ID, b.ID),
		)
	})
}

func boolRank(value bool) int {
	if value {
		return 1
	}
	return 0
}

// Les clés par lesquelles les fiches se regroupent : une machine et un projet
// ; une machine, un projet et un service Compose ; une machine et un nom de
// conteneur.
func projectKey(fiche container.Container) string {
	return composeKey(fiche.MachineID, fiche.Group, "")
}

func composeFicheKey(fiche container.Container) string {
	return composeKey(fiche.MachineID, fiche.Group, fiche.ComposeService)
}

func nameKeyOfFiche(fiche container.Container) string {
	return nameKey(fiche.MachineID, fiche.Name)
}

// groupFiches range les fiches par clé, dans l'ordre où les clés paraissent :
// les fiches arrivent triées, le résultat l'est aussi.
func groupFiches(fiches []container.Container, key func(container.Container) string) [][]container.Container {
	var groups [][]container.Container
	position := map[string]int{}
	for _, fiche := range fiches {
		k := key(fiche)
		index, seen := position[k]
		if !seen {
			index = len(groups)
			position[k] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], fiche)
	}
	return groups
}
