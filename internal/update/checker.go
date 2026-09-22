package update

import (
	"context"
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/registry"
)

// ImageInspector est ce que le vérificateur demande à Docker : l'image
// telle que la machine l'a tirée.
type ImageInspector interface {
	InspectImage(ctx context.Context, reference string) (dockerapi.Image, error)
}

// Registry est ce que le vérificateur demande au registre.
type Registry interface {
	Tags(ctx context.Context, ref registry.Reference) ([]string, error)
	Digest(ctx context.Context, ref registry.Reference) (string, error)
}

// Checker vérifie une image : ce que la machine a tiré, ce que le
// registre publie.
type Checker struct {
	docker   ImageInspector
	registry Registry
}

func NewChecker(docker ImageInspector, remote Registry) *Checker {
	return &Checker{docker: docker, registry: remote}
}

// Check constate. Une erreur ne vient que de Docker : sans lui on ne
// sait rien de l'image, et on ne dit rien ; le registre, lui, répond
// toujours par une issue.
func (c *Checker) Check(ctx context.Context, image string, now time.Time) (Result, error) {
	result := Result{Image: image, CheckedAt: now.UTC().Truncate(time.Second)}
	ref, err := registry.Parse(image)
	if err != nil || ref.Tag == "" {
		result.Outcome = OutcomeUnsupported
		return result, nil
	}
	inspected, err := c.docker.InspectImage(ctx, image)
	if err != nil {
		return Result{}, err
	}
	result.LocalDigest = inspected.PulledDigest(ref.Familiar())
	if result.LocalDigest == "" {
		result.Outcome = OutcomeLocal
		return result, nil
	}
	tags, err := c.registry.Tags(ctx, ref)
	if err != nil {
		result.Outcome = outcomeOf(err)
		return result, nil
	}
	result.RemoteDigest, err = c.registry.Digest(ctx, ref)
	if err != nil {
		result.Outcome = outcomeOf(err)
		return result, nil
	}
	result.Outcome = OutcomeOK
	result.NewerTag = Newer(ref.Tag, tags)
	if result.NewerTag == "" {
		return result, nil
	}
	// L'empreinte du tag plus récent est un fait de plus, pas une
	// condition : sans elle, la mise à jour se voit quand même.
	result.NewerDigest, _ = c.registry.Digest(ctx, ref.WithTag(result.NewerTag))
	return result, nil
}

func outcomeOf(err error) Outcome {
	switch {
	case errors.Is(err, registry.ErrUnauthorized):
		return OutcomeUnauthorized
	case errors.Is(err, registry.ErrNotFound):
		return OutcomeNotFound
	default:
		return OutcomeUnreachable
	}
}
