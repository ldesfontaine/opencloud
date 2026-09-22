package registry

import (
	"errors"
	"regexp"
	"strings"
)

// ErrBadReference : une image que Docker lui-même ne saurait pas nommer.
var ErrBadReference = errors.New("bad image reference")

const (
	// Docker Hub : « docker.io » et « index.docker.io » sont des alias
	// que l'API v2 ne sert pas ; c'est celui-ci qui répond.
	DockerHub = "registry-1.docker.io"
	// Les images officielles du Hub vivent sous ce préfixe que la ligne de
	// commande cache.
	dockerLibrary = "library/"
	DefaultTag    = "latest"
)

var (
	repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	tagPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Reference est une image telle que le registre la nomme : l'hôte à
// joindre, le dépôt complet et le tag. Digest est posé quand l'image est
// désignée par son empreinte : un tel service ne se met pas à jour tout
// seul, il n'y a rien à comparer.
type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

// Parse lit ce que Docker écrit dans « Image » : « alpine:3.20 »,
// « ghcr.io/org/app:v1 », « localhost:5000/app », « nginx@sha256:… ».
func Parse(image string) (Reference, error) {
	name, digest, hasDigest := strings.Cut(image, "@")
	if hasDigest && !digestPattern.MatchString(digest) {
		return Reference{}, ErrBadReference
	}
	name, tag, hasTag := splitTag(name)
	if hasTag && tag == "" {
		return Reference{}, ErrBadReference
	}
	registry, repository := splitRegistry(name)
	if registry == DockerHub && !strings.Contains(repository, "/") {
		repository = dockerLibrary + repository
	}
	if !repositoryPattern.MatchString(repository) || !isRegistryHost(registry) {
		return Reference{}, ErrBadReference
	}
	if tag == "" && !hasDigest {
		tag = DefaultTag
	}
	if tag != "" && !tagPattern.MatchString(tag) {
		return Reference{}, ErrBadReference
	}
	return Reference{Registry: registry, Repository: repository, Tag: tag, Digest: digest}, nil
}

// splitTag sépare le tag : ce qui suit le dernier « : », sauf si une
// barre oblique le suit encore, auquel cas c'est le port du registre.
func splitTag(name string) (string, string, bool) {
	index := strings.LastIndex(name, ":")
	if index < 0 || strings.Contains(name[index+1:], "/") {
		return name, "", false
	}
	return name[:index], name[index+1:], true
}

// splitRegistry lit l'hôte devant la première barre oblique s'il en a
// l'air (un point, un port, ou « localhost ») ; sinon c'est Docker Hub.
func splitRegistry(name string) (string, string) {
	host, rest, found := strings.Cut(name, "/")
	if !found || !(strings.ContainsAny(host, ".:") || host == "localhost") {
		return DockerHub, name
	}
	if host == "docker.io" || host == "index.docker.io" {
		return DockerHub, rest
	}
	return host, rest
}

func isRegistryHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/@ ") {
		return false
	}
	return !strings.HasPrefix(host, ":") && !strings.HasSuffix(host, ":")
}

// Familiar rend le nom tel que Docker l'écrit dans RepoDigests : sans
// l'hôte ni « library/ » pour le Hub, avec l'hôte ailleurs.
func (r Reference) Familiar() string {
	if r.Registry != DockerHub {
		return r.Registry + "/" + r.Repository
	}
	return strings.TrimPrefix(r.Repository, dockerLibrary)
}

// String rend l'image avec son tag, comme on la tire.
func (r Reference) String() string {
	if r.Tag == "" {
		return r.Familiar() + "@" + r.Digest
	}
	return r.Familiar() + ":" + r.Tag
}

// WithTag rend la même image sur un autre tag.
func (r Reference) WithTag(tag string) Reference {
	return Reference{Registry: r.Registry, Repository: r.Repository, Tag: tag}
}
