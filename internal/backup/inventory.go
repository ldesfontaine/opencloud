package backup

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Les genres de source de l'inventaire.
const (
	KindDatabase = "database"
	KindVM       = "vm"
	KindLXC      = "lxc"
	KindVolume   = "docker_volume"

	// Les genres de cible : une destination d'openCloud (une machine avec agent
	// et un dossier), ou un stockage que Proxmox accepte pour ses sauvegardes.
	TargetDestination = "destination"
	TargetStorage     = "storage"
)

// SourceKinds rend la liste fermée des genres de source.
func SourceKinds() []string {
	return []string{KindDatabase, KindVM, KindLXC, KindVolume}
}

// TargetKinds rend la liste fermée des genres de cible.
func TargetKinds() []string {
	return []string{TargetDestination, TargetStorage}
}

// ObservedGuest est une VM ou un conteneur LXC d'un hôte Proxmox, tel que son
// dernier relevé l'a vu, avec ce que les stockages gardent de lui. Les modèles
// n'y sont pas : vzdump n'en sauvegarde pas.
type ObservedGuest struct {
	HostMachineID   string
	HostMachineName string
	VMID            int
	Kind            string
	Name            string
	Node            string
	Archives        ObservedArchives
}

// ObservedArchives sont les fichiers vzdump vus sur les stockages de l'hôte,
// rattachés à l'invité par le numéro de leur nom. Zéro archive : rien vu.
type ObservedArchives struct {
	Count      int
	TotalBytes int64
	// La plus récente : sa date, sa taille, et le stockage où elle est.
	LastAt    time.Time
	LastBytes int64
	Storage   string
}

// ObservedStorage est un stockage d'un hôte Proxmox qui accepte des
// sauvegardes.
type ObservedStorage struct {
	HostMachineID   string
	HostMachineName string
	Node            string
	Name            string
	Type            string
	Total           int64
	Used            int64
	Avail           int64
	Active          bool
}

// ObservedVolume est un volume nommé Docker d'une machine, tel que son agent
// l'a listé.
type ObservedVolume struct {
	MachineID   string
	MachineName string
	Name        string
	Driver      string
	UsedBy      []string
}

// LatestRuns sont, par clé de source, la dernière ligne de l'historique et la
// dernière réussie.
type LatestRuns struct {
	Last   map[string]RunRecord
	LastOK map[string]RunRecord
}

// Coverage dit quel plan d'openCloud couvre une source, et où il l'envoie.
type Coverage struct {
	Subject         string
	PlanName        string
	DestinationID   string
	DestinationName string
}

// Source est ce qui peut être sauvegardé : la base d'openCloud, une VM ou un
// LXC d'un hôte Proxmox, un volume Docker d'une machine.
type Source struct {
	Key         string
	Kind        string
	Name        string
	MachineID   string
	MachineName string
	// L'identifiant propre au genre : le numéro de l'invité ; vide sinon.
	ID string
	// Pour un volume : son pilote et les conteneurs qui le montent.
	Driver string
	UsedBy []string
	// Les plans d'openCloud qui la couvrent aujourd'hui.
	CoveredBy []Coverage
	// Ce que les stockages de Proxmox gardent d'elle, hors des plans
	// d'openCloud ; nil pour une source qui n'est pas un invité, ou rien vu.
	Observed *ObservedArchives
	// La dernière exécution d'un plan d'openCloud sur cette source, et la
	// dernière réussie ; nil tant qu'aucune n'a eu lieu.
	LastRun *RunRecord
	LastOK  *RunRecord
}

// Target est un endroit où une sauvegarde peut aller. Une destination
// d'openCloud et un stockage de Proxmox peuvent être le même endroit physique :
// l'inventaire ne les fusionne pas, rien ne dit qu'ils le sont.
type Target struct {
	Key  string
	Kind string
	Name string
	// La machine de la destination, ou l'hôte Proxmox du stockage.
	MachineID   string
	MachineName string
	// Destination : son dossier et l'état du droit de réception.
	Path        string
	Right       RightState
	RightReason string
	// Stockage : son nœud, son type, sa capacité et s'il répond.
	Node   string
	Type   string
	Total  int64
	Used   int64
	Avail  int64
	Active bool
	// Les plans d'openCloud qui envoient vers cette destination.
	Plans []string
}

// Inventory est ce qui existe à sauvegarder, où cela peut aller, et ce qui s'est
// passé. Il se calcule de ce que le serveur a déjà en base : aucun appel à une
// machine ni à Proxmox.
type Inventory struct {
	Sources []Source
	Targets []Target
}

// Inventory calcule l'inventaire des sources et des cibles. Les sources que
// rien ne sauvegarde viennent en tête, puis celles que seul Proxmox sauvegarde,
// puis celles qu'un plan d'openCloud couvre.
func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	plans, err := s.store.ListBackupPlans(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("list backup plans: %w", err)
	}
	destinations, err := s.store.ListBackupDestinations(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("list destinations: %w", err)
	}
	guests, err := s.store.ListObservedGuests(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("list observed guests: %w", err)
	}
	storages, err := s.store.ListObservedStorages(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("list observed storages: %w", err)
	}
	volumes, err := s.store.ListObservedVolumes(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("list observed volumes: %w", err)
	}
	latest, err := s.store.LatestRunsBySource(ctx)
	if err != nil {
		return Inventory{}, fmt.Errorf("read latest backup runs: %w", err)
	}
	destinationNames := make(map[string]string, len(destinations))
	for _, destination := range destinations {
		destinationNames[destination.ID] = destination.Name
	}

	sources := []Source{s.databaseSource(ctx)}
	for _, guest := range guests {
		sources = append(sources, guestSource(guest))
	}
	for _, volume := range volumes {
		sources = append(sources, volumeSource(volume))
	}
	for index := range sources {
		source := &sources[index]
		source.CoveredBy = coverageOf(*source, plans, destinationNames)
		if run, found := latest.Last[source.Key]; found {
			source.LastRun = &run
		}
		if run, found := latest.LastOK[source.Key]; found {
			source.LastOK = &run
		}
	}
	slices.SortStableFunc(sources, func(a, b Source) int { return protectionOf(a) - protectionOf(b) })

	return Inventory{Sources: sources, Targets: s.targets(ctx, destinations, storages, plans)}, nil
}

// protectionOf ordonne les sources : 0 rien ne la sauvegarde, 1 seul Proxmox la
// sauvegarde (hors d'openCloud), 2 un plan d'openCloud la couvre.
func protectionOf(source Source) int {
	switch {
	case len(source.CoveredBy) > 0:
		return 2
	case source.Observed != nil:
		return 1
	}
	return 0
}

func (s *Service) databaseSource(ctx context.Context) Source {
	source := Source{Key: SourceDatabaseKey, Kind: KindDatabase, Name: brand.ID, MachineID: machine.LocalID}
	// Le nom de la machine ne se lit pas toujours (une base neuve) : la source
	// existe quand même.
	if local, err := s.machines.Get(ctx, machine.LocalID); err == nil {
		source.MachineName = local.Name
	}
	return source
}

func guestSource(guest ObservedGuest) Source {
	source := Source{
		Key: GuestSourceKey(guest.HostMachineID, guest.VMID), Kind: guest.Kind, Name: guest.Name,
		MachineID: guest.HostMachineID, MachineName: guest.HostMachineName, ID: strconv.Itoa(guest.VMID),
	}
	if guest.Archives.Count > 0 {
		archives := guest.Archives
		source.Observed = &archives
	}
	return source
}

func volumeSource(volume ObservedVolume) Source {
	return Source{
		Key: VolumeSourceKey(volume.MachineID, volume.Name), Kind: KindVolume, Name: volume.Name,
		MachineID: volume.MachineID, MachineName: volume.MachineName, Driver: volume.Driver, UsedBy: volume.UsedBy,
	}
}

// coverageOf rend les plans d'openCloud qui couvrent la source aujourd'hui : le
// plan de la base pour la base, un plan machine pour les invités de l'hôte
// qu'il nomme, tous pour « all », ceux de la liste sinon. Aucun producteur ne
// sauvegarde encore un volume Docker.
func coverageOf(source Source, plans []Plan, destinationNames map[string]string) []Coverage {
	var covering []Coverage
	for _, plan := range plans {
		if !planCovers(plan, source) {
			continue
		}
		covering = append(covering, Coverage{
			Subject: plan.Subject, PlanName: plan.Name, DestinationID: plan.DestinationID,
			DestinationName: destinationNames[plan.DestinationID],
		})
	}
	return covering
}

func planCovers(plan Plan, source Source) bool {
	switch {
	case plan.SourceKind == SourceDatabase:
		return source.Kind == KindDatabase
	case plan.SourceKind == SourceAgent && plan.Producer == machine.ProducerProxmoxVzdump:
		if source.Kind != KindVM && source.Kind != KindLXC || source.MachineID != plan.SourceMachineID {
			return false
		}
		return plan.Selector == SelectorAll || slices.Contains(listedTargets(plan.Selector), source.ID)
	}
	return false
}

// targets assemble les cibles : les destinations d'openCloud, puis les
// stockages de Proxmox qui acceptent des sauvegardes.
func (s *Service) targets(ctx context.Context, destinations []Destination, storages []ObservedStorage, plans []Plan) []Target {
	targets := make([]Target, 0, len(destinations)+len(storages))
	for _, destination := range destinations {
		target := Target{
			Key: "destination:" + destination.ID, Kind: TargetDestination, Name: destination.Name,
			MachineID: destination.MachineID, MachineName: destination.MachineName, Path: destination.Path, Right: RightUnknown,
		}
		if machineStatus, err := s.machines.Get(ctx, destination.MachineID); err == nil {
			target.Right, target.RightReason = rightOf(machineStatus, destination.Path)
		}
		for _, plan := range plans {
			if plan.DestinationID == destination.ID {
				target.Plans = append(target.Plans, plan.Subject)
			}
		}
		targets = append(targets, target)
	}
	for _, storage := range storages {
		targets = append(targets, Target{
			Key: strings.Join([]string{"storage", storage.HostMachineID, storage.Node, storage.Name}, ":"), Kind: TargetStorage,
			Name: storage.Name, MachineID: storage.HostMachineID, MachineName: storage.HostMachineName, Node: storage.Node,
			Type: storage.Type, Total: storage.Total, Used: storage.Used, Avail: storage.Avail, Active: storage.Active,
		})
	}
	return targets
}
