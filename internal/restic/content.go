package restic

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// La borne de l'ordre (agentwire) : au plus ce nombre de volumes, et de
// bases, par service.
const maxServiceParts = 32

// ServiceContent est ce que « le service entier » sauvegarde : son dossier
// Compose (vide s'il n'en a pas), ses volumes nommés et ses bases détectées.
// Il se dérive de l'inventaire à chaque envoi : un volume ajouté au service
// entre dans la nuit suivante sans rien redéclarer.
type ServiceContent struct {
	Folder    string
	Volumes   []string
	Databases []Database
}

// Database est une base d'un service : le conteneur qui la porte, et le
// moteur que son image désigne.
type Database struct {
	Container string
	Engine    string
}

// IsEmpty : rien à sauvegarder, ni dossier, ni volume, ni base.
func (c ServiceContent) IsEmpty() bool {
	return c.Folder == "" && len(c.Volumes) == 0 && len(c.Databases) == 0
}

// NeedsDocker : un volume ou une base passe par restic.docker.
func (c ServiceContent) NeedsDocker() bool {
	return len(c.Volumes) > 0 || len(c.Databases) > 0
}

// ServiceDocker est ce que l'inventaire Docker de sa machine dit d'un
// service, brut : les conteneurs rattachés à ses unités, et les volumes de la
// machine avec les conteneurs qui les montent.
type ServiceDocker struct {
	Containers []DockerContainer
	Volumes    []DockerVolume
}

type DockerContainer struct {
	Name  string
	Image string
}

type DockerVolume struct {
	Name   string
	UsedBy []string
}

// deriveContent compose le contenu d'un service : le dossier s'il est un
// dossier que l'agent accepte, ses bases, et les volumes qu'un de ses
// conteneurs monte. Triés, bornés ; isTruncated dit qu'il a fallu couper.
func deriveContent(service ServiceRef, docker ServiceDocker) (content ServiceContent, isTruncated bool) {
	if isServiceFolder(service.Project) {
		content.Folder = service.Project
	}
	content.Databases = databasesOf(docker.Containers)
	content.Volumes = volumesMountedBy(docker.Volumes, docker.Containers)
	isTruncated = len(content.Volumes) > maxServiceParts || len(content.Databases) > maxServiceParts
	content.Volumes = content.Volumes[:min(len(content.Volumes), maxServiceParts)]
	content.Databases = content.Databases[:min(len(content.Databases), maxServiceParts)]
	return content, isTruncated
}

// databasesOf : les conteneurs dont l'image est une base prise en charge,
// par nom.
func databasesOf(containers []DockerContainer) []Database {
	databases := []Database{}
	for _, candidate := range containers {
		engine := agentwire.DatabaseEngine(candidate.Image)
		if engine != "" && agentwire.IsDockerName(candidate.Name) {
			databases = append(databases, Database{Container: candidate.Name, Engine: engine})
		}
	}
	slices.SortFunc(databases, func(a, b Database) int { return strings.Compare(a.Container, b.Container) })
	return slices.CompactFunc(databases, func(a, b Database) bool { return a.Container == b.Container })
}

// volumesMountedBy : les volumes nommés qu'un des conteneurs monte, par nom.
func volumesMountedBy(volumes []DockerVolume, containers []DockerContainer) []string {
	isMember := map[string]bool{}
	for _, candidate := range containers {
		isMember[candidate.Name] = true
	}
	names := []string{}
	for _, volume := range volumes {
		if agentwire.IsDockerName(volume.Name) && slices.ContainsFunc(volume.UsedBy, func(name string) bool { return isMember[name] }) {
			names = append(names, volume.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// serviceContent lit l'inventaire du service et en dérive le contenu ; une
// coupe à la borne se journalise, l'ordre part avec ce qui tient.
func (s *Service) serviceContent(ctx context.Context, service ServiceRef) (ServiceContent, error) {
	docker, err := s.store.ResticServiceDocker(ctx, service.ID)
	if err != nil {
		return ServiceContent{}, fmt.Errorf("read service docker inventory: %w", err)
	}
	content, isTruncated := deriveContent(service, docker)
	if isTruncated {
		s.logger.Warn("restic service content truncated", "service_id", service.ID, "limit", maxServiceParts)
	}
	return content, nil
}
