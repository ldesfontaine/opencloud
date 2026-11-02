package agent

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Une sauvegarde de service se fait en parties : son dossier, chacun de ses
// volumes, chacune de ses bases. Chaque partie est son propre instantané,
// sous un nom stable ; l'échec de l'une n'arrête pas les suivantes. Un
// invité Proxmox est une seule partie, sans bilan de partie.

// dockerInventory est ce que la sauvegarde lit de Docker pour retrouver un
// volume et une base. Jamais l'environnement d'un conteneur : dockerapi ne le
// décode pas hors observe.env, et la sauvegarde ne s'en sert pas.
type dockerInventory interface {
	ListContainers(ctx context.Context) ([]dockerapi.Summary, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
	ListVolumes(ctx context.Context) ([]dockerapi.Volume, error)
}

// backupPart est une partie à sauvegarder et ce qu'elle a donné.
type backupPart struct {
	// report : le bilan qui part au serveur ; Kind vide pour un invité, qui
	// n'en rapporte pas.
	report agentwire.ResticPartReport
	// directory pour le dossier ; sinon filename et producer, le flux que
	// restic lance. Sans l'un ni l'autre, la partie ne se lance pas.
	directory string
	filename  string
	producer  []string
	// Ce que la dernière exécution a laissé.
	snapshotID string
	lines      []agentwire.OutputLine
}

func (p backupPart) isRunnable() bool {
	return p.directory != "" || len(p.producer) > 0
}

// isPending : la partie se lance (encore) à cette passe.
func (p backupPart) isPending() bool {
	return p.isRunnable() && p.report.State != agentwire.ResticPartOK
}

// command rend la commande restic de la partie.
func (p backupPart) command(binary string, repository restic.Repository) (restic.Command, error) {
	if p.directory != "" {
		return restic.BackupDirectory(binary, repository, p.directory)
	}
	return restic.BackupStream(binary, repository, p.filename, p.producer)
}

// label nomme la partie dans la sortie de l'ordre : son genre et son nom.
func (p backupPart) label() string {
	return strings.TrimSpace(p.report.Kind + " " + p.report.Name)
}

func skippedPart(kind, name, reason string) backupPart {
	return backupPart{report: agentwire.ResticPartReport{Kind: kind, Name: name, State: agentwire.ResticPartSkipped, Reason: reason}}
}

func failedPart(kind, name, reason string) backupPart {
	return backupPart{report: agentwire.ResticPartReport{Kind: kind, Name: name, State: agentwire.ResticPartFailed, Reason: reason}}
}

// serviceParts rend les parties d'un service : le dossier s'il en a un, puis
// ses volumes, puis ses bases. Docker se rejuge avant d'y toucher.
func (s *resticSource) serviceParts(ctx context.Context, job resticBackupJob, checks hostChecks) ([]backupPart, error) {
	var parts []backupPart
	if job.directory != "" {
		parts = append(parts, backupPart{report: agentwire.ResticPartReport{Kind: agentwire.ResticPartFolder}, directory: job.directory})
	}
	if !needsDocker(job.params) {
		return parts, nil
	}
	if reason := checks.docker(ctx); reason != "" {
		return nil, refuseWith(reason, errors.New("docker cannot be used by the agent"))
	}
	containers, volumes, err := s.readDocker(ctx)
	if err != nil {
		s.logger.Warn("docker inventory unreadable", "error", err)
		for _, name := range job.params.Volumes {
			parts = append(parts, failedPart(agentwire.ResticPartVolume, name, agentwire.ReasonDockerMissing))
		}
		for _, name := range job.params.Databases {
			parts = append(parts, failedPart(agentwire.ResticPartDatabase, name, agentwire.ReasonDockerMissing))
		}
		return parts, nil
	}
	for _, name := range job.params.Volumes {
		parts = append(parts, s.volumePart(name, volumes, containers))
	}
	for _, name := range job.params.Databases {
		parts = append(parts, s.databasePart(name, containers))
	}
	return parts, nil
}

// needsDocker : l'ordre porte des volumes ou des bases (droit restic.docker).
func needsDocker(params agentwire.ResticBackupParams) bool {
	return len(params.Volumes) > 0 || len(params.Databases) > 0
}

// readDocker relit chez l'agent les conteneurs (inspectés, rangés par nom)
// et les noms des volumes : ce que le serveur a envoyé ne vaut que s'il y est.
func (s *resticSource) readDocker(ctx context.Context) ([]dockerapi.Container, []string, error) {
	if s.inventory == nil {
		return nil, nil, errors.New("no docker inventory")
	}
	summaries, err := s.inventory.ListContainers(ctx)
	if err != nil {
		return nil, nil, err
	}
	containers := make([]dockerapi.Container, 0, len(summaries))
	for _, summary := range summaries {
		inspected, err := s.inventory.Inspect(ctx, summary.ID)
		if errors.Is(err, dockerapi.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		containers = append(containers, inspected)
	}
	slices.SortFunc(containers, func(a, b dockerapi.Container) int { return strings.Compare(a.ShortName(), b.ShortName()) })
	listed, err := s.inventory.ListVolumes(ctx)
	if err != nil {
		return nil, nil, err
	}
	volumes := make([]string, 0, len(listed))
	for _, volume := range listed {
		volumes = append(volumes, volume.Name)
	}
	return containers, volumes, nil
}

// volumePart lit un volume par le premier conteneur (par nom) qui le monte :
// l'agent n'est pas root, il ne lit pas /var/lib/docker.
func (s *resticSource) volumePart(name string, volumes []string, containers []dockerapi.Container) backupPart {
	if !agentwire.IsDockerName(name) || !slices.Contains(volumes, name) {
		return skippedPart(agentwire.ResticPartVolume, name, agentwire.ReasonContainerMissing)
	}
	for _, container := range containers {
		for _, mount := range container.Mounts {
			if mount.Type != "volume" || mount.Name != name {
				continue
			}
			producer, err := restic.VolumeProducer(s.dockerProgram, container.ShortName(), mount.Destination)
			if err != nil {
				continue
			}
			part := backupPart{filename: restic.VolumeFileName(name), producer: producer}
			part.report = agentwire.ResticPartReport{Kind: agentwire.ResticPartVolume, Name: name}
			return part
		}
	}
	return skippedPart(agentwire.ResticPartVolume, name, agentwire.ReasonVolumeUnmounted)
}

// databasePart vide une base dans son conteneur, qui doit tourner : le
// moteur se lit au nom de l'image, jamais à l'environnement.
func (s *resticSource) databasePart(name string, containers []dockerapi.Container) backupPart {
	index := slices.IndexFunc(containers, func(c dockerapi.Container) bool { return c.ShortName() == name })
	if !agentwire.IsDockerName(name) || index < 0 {
		return skippedPart(agentwire.ResticPartDatabase, name, agentwire.ReasonContainerMissing)
	}
	container := containers[index]
	engine := agentwire.DatabaseEngine(container.Config.Image)
	if engine == "" {
		return skippedPart(agentwire.ResticPartDatabase, name, agentwire.ReasonEngineUnsupported)
	}
	if container.State.Status != "running" {
		return failedPart(agentwire.ResticPartDatabase, name, agentwire.ReasonDumpFailed)
	}
	producer, err := restic.DatabaseProducer(s.dockerProgram, name, engine)
	if err != nil {
		return failedPart(agentwire.ResticPartDatabase, name, agentwire.ReasonDumpFailed)
	}
	part := backupPart{filename: restic.DatabaseFileName(name, engine), producer: producer}
	part.report = agentwire.ResticPartReport{Kind: agentwire.ResticPartDatabase, Name: name}
	return part
}

// failureReason dit pourquoi une partie lancée a échoué : le script d'une
// base signale l'échec de son outil (restic.DumpFailedMarker) ; le reste est
// un échec de restic.
func failureReason(part backupPart) string {
	if part.report.Kind == agentwire.ResticPartDatabase && hasLine(part.lines, restic.DumpFailedMarker) {
		return agentwire.ReasonDumpFailed
	}
	return agentwire.ReasonResticFailed
}

func hasLine(lines []agentwire.OutputLine, text string) bool {
	return slices.ContainsFunc(lines, func(line agentwire.OutputLine) bool { return strings.Contains(line.Line, text) })
}
