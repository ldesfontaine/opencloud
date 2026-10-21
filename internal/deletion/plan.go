package deletion

import "fmt"

// Kind est ce qu'une ligne du plan compte ou nomme ; le front en tire le
// libellé (deletion.kind_<genre>).
type Kind string

const (
	// Ce qui retient : des objets déclarés, nommés un par un.
	KindDeclaredServices Kind = "declared_services"
	KindProbes           Kind = "probes"
	KindHeartbeats       Kind = "heartbeats"
	KindComponents       Kind = "components"
	KindStatusPages      Kind = "status_pages"

	// Ce qui part avec l'objet, ou s'en détache : des lignes comptées.
	KindEnrollmentTokens Kind = "enrollment_tokens"
	KindMachineSamples   Kind = "machine_samples"
	KindMachineNetworks  Kind = "machine_networks"
	KindContainers       Kind = "containers"
	KindContainerEvents  Kind = "container_events"
	KindImageChecks      Kind = "image_checks"
	KindIgnoredKeys      Kind = "ignored_keys"
	KindSilences         Kind = "silences"
	KindProbeResults     Kind = "probe_results"
	KindProbeDays        Kind = "probe_days"
	KindHeartbeatPings   Kind = "heartbeat_pings"
	KindHeartbeatRuns    Kind = "heartbeat_runs"
	KindUnits            Kind = "units"
	KindMembers          Kind = "members"
	KindAlerts           Kind = "alerts"
	KindProjectLinks     Kind = "project_links"
	KindIncidentLinks    Kind = "incident_links"
)

// Kinds rend la liste fermée : chaque genre a son libellé dans les catalogues.
func Kinds() []Kind {
	return []Kind{
		KindDeclaredServices, KindProbes, KindHeartbeats, KindComponents, KindStatusPages,
		KindEnrollmentTokens, KindMachineSamples, KindMachineNetworks, KindContainers,
		KindContainerEvents, KindImageChecks, KindIgnoredKeys, KindSilences,
		KindProbeResults, KindProbeDays, KindHeartbeatPings, KindHeartbeatRuns,
		KindUnits, KindMembers, KindAlerts, KindProjectLinks, KindIncidentLinks,
	}
}

// Object est un objet déclaré qui retient la suppression : l'opérateur doit
// le supprimer ou le détacher d'abord.
type Object struct {
	ID   string
	Name string
}

// Blocker est un genre d'objets qui retient la suppression, avec chacun d'eux.
type Blocker struct {
	Kind    Kind
	Objects []Object
}

// Count est un genre de lignes emportées ou détachées, et leur nombre.
type Count struct {
	Kind  Kind
	Count int
}

// Plan dit ce qu'une suppression ferait. Tant qu'il a un bloqueur, rien ne
// part. Seuls les genres non vides y figurent.
type Plan struct {
	Blockers []Blocker
	Removes  []Count
	Detaches []Count
}

func (p Plan) IsBlocked() bool {
	return len(p.Blockers) > 0
}

// Block ajoute les objets qui retiennent, s'il y en a.
func (p *Plan) Block(kind Kind, objects []Object) {
	if len(objects) > 0 {
		p.Blockers = append(p.Blockers, Blocker{Kind: kind, Objects: objects})
	}
}

// Remove ajoute ce qui part avec l'objet, s'il y en a.
func (p *Plan) Remove(kind Kind, count int) {
	if count > 0 {
		p.Removes = append(p.Removes, Count{Kind: kind, Count: count})
	}
}

// Detach ajoute ce qui reste mais perd son lien vers l'objet, s'il y en a.
func (p *Plan) Detach(kind Kind, count int) {
	if count > 0 {
		p.Detaches = append(p.Detaches, Count{Kind: kind, Count: count})
	}
}

// RefusedError est le refus d'une suppression bloquée : la raison est le
// refus du domaine (sa clé), le plan dit ce qui retient.
type RefusedError struct {
	Plan   Plan
	Reason error
}

func (e *RefusedError) Error() string {
	return e.Reason.Error()
}

// Unwrap rend le refus du domaine : errors.Is le reconnaît, refusal.KeyOf y
// lit la clé.
func (e *RefusedError) Unwrap() error {
	return e.Reason
}

// Refuse rend le refus d'un plan bloqué, nil sinon. reasons donne le refus
// que le domaine déclare pour chaque genre qui peut le retenir : la clé
// rendue est celle du premier bloqueur. Un genre sans refus déclaré est une
// faute du code, pas un refus : l'erreur n'a pas de clé.
func Refuse(plan Plan, reasons map[Kind]error) error {
	if !plan.IsBlocked() {
		return nil
	}
	first := plan.Blockers[0].Kind
	reason, declared := reasons[first]
	if !declared {
		reason = fmt.Errorf("deletion blocked by %s, which has no declared refusal", first)
	}
	return &RefusedError{Plan: plan, Reason: reason}
}
