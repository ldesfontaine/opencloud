package manifest

import (
	"slices"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// pendingPrefix marque, dans une définition du plan, l'identifiant d'un objet
// que ce même plan crée : il n'existe pas encore, seul son nom le désigne.
const pendingPrefix = "new:"

// labels redonne un nom, dans le vocabulaire du fichier, aux identifiants des
// définitions comparées : la machine par son nom, le projet par le sien, le
// service par « machine/nom ».
type labels struct {
	machines map[string]string
	projects map[string]string
	services map[string]string
}

func (l labels) machine(id string) string {
	return lookupLabel(l.machines, id)
}

func (l labels) project(id string) string {
	return lookupLabel(l.projects, id)
}

func (l labels) service(id string) string {
	return lookupLabel(l.services, id)
}

// lookupLabel rend le nom de l'identifiant ; un identifiant en attente se
// lit par son nom, un inconnu tel quel (il n'a pas de nom à montrer).
func lookupLabel(names map[string]string, id string) string {
	if name, found := names[id]; found {
		return name
	}
	return strings.TrimPrefix(id, pendingPrefix)
}

// fieldDiff accumule les champs qui changent, dans l'ordre où le fichier les
// écrit.
type fieldDiff struct {
	fields []FieldChange
}

func (d *fieldDiff) text(field, before, after string) {
	if before != after {
		d.fields = append(d.fields, FieldChange{Field: field, Before: before, After: after})
	}
}

func (d *fieldDiff) list(field string, before, after []string) {
	if !slices.Equal(before, after) {
		d.fields = append(d.fields, FieldChange{Field: field, Before: joinLabels(before), After: joinLabels(after)})
	}
}

func (d *fieldDiff) number(field string, before, after int) {
	if before != after {
		d.fields = append(d.fields, FieldChange{Field: field, Before: strconv.Itoa(before), After: strconv.Itoa(after)})
	}
}

// flag écrit un booléen comme le fichier : rien tant qu'il est faux.
func (d *fieldDiff) flag(field string, before, after bool) {
	if before != after {
		d.fields = append(d.fields, FieldChange{Field: field, Before: flagText(before), After: flagText(after)})
	}
}

func flagText(value bool) string {
	if value {
		return "true"
	}
	return ""
}

func (d *fieldDiff) add(field, after string) {
	d.fields = append(d.fields, FieldChange{Field: field, After: after})
}

func (d *fieldDiff) remove(field, before string) {
	d.fields = append(d.fields, FieldChange{Field: field, Before: before})
}

func diffProject(before, after directory.ProjectDefinition) []FieldChange {
	var diff fieldDiff
	diff.text("name", before.Name, after.Name)
	diff.text("title", before.Title, after.Title)
	return diff.fields
}

// serviceDiff est ce qui sépare deux définitions de service.
type serviceDiff struct {
	Fields []FieldChange
	Notes  []Note
}

// diffService compare deux définitions complétées. Les ensembles (provides,
// projects) se comparent comme des ensembles ; les unités s'apparient par
// nom, et leur ordre compte : il est stocké.
func diffService(before, after directory.ServiceDefinition, names labels) serviceDiff {
	var diff fieldDiff
	var notes []Note
	if before.MachineID != after.MachineID {
		diff.text("machine", names.machine(before.MachineID), names.machine(after.MachineID))
		notes = append(notes, NoteServiceMoves)
	}
	diff.text("name", before.Name, after.Name)
	diff.text("title", before.Title, after.Title)
	diff.text("nature", string(before.Nature), string(after.Nature))
	diff.text("origin", string(before.Origin), string(after.Origin))
	diff.text("compose_project", before.ComposeProject, after.ComposeProject)
	diff.text("public_url", before.PublicURL, after.PublicURL)
	diff.list("provides", capabilityNames(before.Provides), capabilityNames(after.Provides))
	diff.list("projects", projectLabels(before.ProjectIDs, names), projectLabels(after.ProjectIDs, names))
	notes = append(notes, diffUnits(&diff, before.Units, after.Units)...)
	return serviceDiff{Fields: diff.fields, Notes: notes}
}

// capabilityNames range les fournitures dans l'ordre de Capabilities(), celui
// du fichier : seul l'ensemble compte.
func capabilityNames(provided []directory.Capability) []string {
	names := []string{}
	for _, capability := range directory.Capabilities() {
		if slices.Contains(provided, capability) {
			names = append(names, string(capability))
		}
	}
	return names
}

// projectLabels nomme les projets d'un service et les range par nom : seul
// l'ensemble compte.
func projectLabels(ids []string, names labels) []string {
	labelled := make([]string, 0, len(ids))
	for _, id := range ids {
		labelled = append(labelled, names.project(id))
	}
	slices.Sort(labelled)
	return labelled
}

func diffUnits(diff *fieldDiff, before, after []directory.UnitDefinition) []Note {
	var notes []Note
	beforeByName := make(map[string]directory.UnitDefinition, len(before))
	for _, unit := range before {
		beforeByName[unit.Name] = unit
	}
	afterNames := make(map[string]bool, len(after))
	for _, unit := range after {
		afterNames[unit.Name] = true
	}
	for _, unit := range after {
		previous, kept := beforeByName[unit.Name]
		if !kept {
			diff.add("units["+unit.Name+"]", unitSummary(unit))
			notes = append(notes, NoteUnitAdded)
			continue
		}
		diffUnit(diff, previous, unit)
	}
	for _, unit := range before {
		if !afterNames[unit.Name] {
			diff.remove("units["+unit.Name+"]", unitSummary(unit))
			notes = append(notes, NoteUnitRemoved)
		}
	}
	diffUnitOrder(diff, before, after, afterNames, beforeByName)
	return notes
}

func diffUnit(diff *fieldDiff, before, after directory.UnitDefinition) {
	prefix := "units[" + after.Name + "]."
	diff.text(prefix+"image", before.Image, after.Image)
	diff.text(prefix+"version", before.Version, after.Version)
	diff.text(prefix+"digest", before.Digest, after.Digest)
	diff.text(prefix+"container_name", before.ContainerName, after.ContainerName)
	diff.text(prefix+"systemd_unit", before.SystemdUnit, after.SystemdUnit)
	diff.list(prefix+"ports", before.Ports, after.Ports)
	diff.text(prefix+"updates", string(before.UpdatePolicy), string(after.UpdatePolicy))
}

// diffUnitOrder dit un changement d'ordre entre les unités qui restent : la
// position est stockée et l'écran la suit.
func diffUnitOrder(diff *fieldDiff, before, after []directory.UnitDefinition, afterNames map[string]bool, beforeByName map[string]directory.UnitDefinition) {
	var keptBefore, keptAfter []string
	for _, unit := range before {
		if afterNames[unit.Name] {
			keptBefore = append(keptBefore, unit.Name)
		}
	}
	for _, unit := range after {
		if _, found := beforeByName[unit.Name]; found {
			keptAfter = append(keptAfter, unit.Name)
		}
	}
	diff.list("units", keptBefore, keptAfter)
}

// unitSummary résume une unité sur une ligne : ce qui la désigne.
func unitSummary(unit directory.UnitDefinition) string {
	parts := []string{}
	for _, part := range []string{unit.Image, unit.Version, unit.ContainerName, unit.SystemdUnit} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if unit.UpdatePolicy != "" && unit.UpdatePolicy != container.PolicyWatch {
		parts = append(parts, "updates="+string(unit.UpdatePolicy))
	}
	return strings.Join(parts, " ")
}

// diffProbe compare deux définitions complétées. La cible est montrée sans
// mot de passe : le plan va dans le journal et à l'écran.
func diffProbe(before, after probe.Definition, names labels) []FieldChange {
	var diff fieldDiff
	diff.text("name", before.Name, after.Name)
	diff.text("runner", names.machine(before.MachineID), names.machine(after.MachineID))
	diff.text("service", serviceLabel(before.ServiceID, names), serviceLabel(after.ServiceID, names))
	diff.text("kind", string(before.Kind), string(after.Kind))
	if before.Target != after.Target {
		shownBefore, shownAfter := probe.RedactTarget(before.Target), probe.RedactTarget(after.Target)
		if shownBefore == shownAfter {
			// Seul le mot de passe change : le champ figure quand même, ses
			// deux valeurs masquées se ressemblent et la note dit pourquoi.
			diff.fields = append(diff.fields, FieldChange{Field: "target", Before: shownBefore, After: shownAfter})
		} else {
			diff.text("target", shownBefore, shownAfter)
		}
	}
	diff.text("interval", formatDuration(before.Interval), formatDuration(after.Interval))
	diff.text("timeout", formatDuration(before.Timeout), formatDuration(after.Timeout))
	diff.number("failure_threshold", before.FailureThreshold, after.FailureThreshold)
	diff.number("recovery_threshold", before.RecoveryThreshold, after.RecoveryThreshold)
	diff.text("method", before.Method, after.Method)
	diff.text("expected_status", before.ExpectedStatus, after.ExpectedStatus)
	diff.text("expected_body", before.ExpectedBody, after.ExpectedBody)
	diff.flag("follow_redirects", before.FollowRedirects, after.FollowRedirects)
	diff.flag("tls", before.TLS, after.TLS)
	return diff.fields
}

func serviceLabel(id string, names labels) string {
	if id == "" {
		return ""
	}
	return names.service(id)
}

func diffHeartbeat(before, after heartbeat.Definition, names labels) []FieldChange {
	var diff fieldDiff
	diff.text("name", before.Name, after.Name)
	diff.text("machine", machineLabel(before.MachineID, names), machineLabel(after.MachineID, names))
	diff.text("every", formatDuration(before.Interval), formatDuration(after.Interval))
	diff.text("grace", formatDuration(before.Grace), formatDuration(after.Grace))
	return diff.fields
}

func machineLabel(id string, names labels) string {
	if id == "" {
		return ""
	}
	return names.machine(id)
}
