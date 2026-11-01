package module

// ID nomme un module. Le libellé et la phrase d'un module sont dans les
// catalogues sous module.<id> et module.<id>_text.
type ID string

const (
	// Les conteneurs Docker de la machine.
	Containers ID = "containers"
	// L'hôte Proxmox que la machine porte.
	Hypervisor ID = "hypervisor"
	// La machine est un invité d'un hôte Proxmox déclaré.
	Guest ID = "guest"
	// Les clés SSH que l'agent a relevées sur la machine.
	SSHKeys ID = "ssh_keys"
	// La disponibilité : les sondes, et les pages publiques des projets
	// publiés. Sur une machine, ses sondes seules.
	Probes ID = "probes"
	// Les pare-feu déclarés.
	Firewall ID = "firewall"
	// Les sauvegardes.
	Backups ID = "backups"
	// Les tâches planifiées.
	Jobs ID = "jobs"
)

// Scope dit ce que porte un module : une machine, ou l'installation entière.
type Scope string

const (
	ScopeMachine      Scope = "machine"
	ScopeInstallation Scope = "installation"
)

// module est une ligne de la liste fermée.
type module struct {
	id     ID
	scopes []Scope
}

// Dans l'ordre où les écrans les montrent. Un module peut vivre dans les deux
// portées (les sondes d'une machine, les sondes de l'installation).
var registry = []module{
	{Containers, []Scope{ScopeMachine}},
	{Hypervisor, []Scope{ScopeMachine}},
	{Guest, []Scope{ScopeMachine}},
	{SSHKeys, []Scope{ScopeMachine}},
	{Probes, []Scope{ScopeMachine, ScopeInstallation}},
	{Firewall, []Scope{ScopeInstallation}},
	{Backups, []Scope{ScopeInstallation}},
	{Jobs, []Scope{ScopeInstallation}},
}

// In rend les modules d'une portée.
func In(scope Scope) []ID {
	var ids []ID
	for _, entry := range registry {
		for _, candidate := range entry.scopes {
			if candidate == scope {
				ids = append(ids, entry.id)
			}
		}
	}
	return ids
}

// State dit si un module de l'installation est configuré : au moins un objet
// du module existe.
type State struct {
	ID         ID
	Configured bool
}
