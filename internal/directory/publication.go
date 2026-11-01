package directory

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Publication est la page publique d'un projet, à /statut/<slug>. Un projet
// est publié quand il a un slug ; le titre de la page est celui du projet.
// La langue vide est celle de l'interface, résolue à la lecture.
type Publication struct {
	Slug         string
	Announcement string
	Language     lang.Code
	PublishedAt  time.Time
}

// IsPublished dit si le projet a sa page.
func (p Publication) IsPublished() bool {
	return p.Slug != ""
}

// PublicationDefinition est ce que l'opérateur règle de la page : un slug
// vide dépublie le projet.
type PublicationDefinition struct {
	Slug         string
	Announcement string
	Language     lang.Code
}

const (
	MaxSlugLength         = 63
	MaxAnnouncementLength = 500
	// Les pages se créent à la main ; chacune se relit à chaque changement.
	MaxPublications = 64
)

var (
	ErrSlugInvalid         = refusal.Invalid("project.publish_slug_invalid", "publication slug must be lowercase letters, digits and dashes, not starting with a dash")
	ErrSlugTaken           = refusal.Invalid("project.publish_slug_taken", "publication slug already used by another project")
	ErrAnnouncementInvalid = refusal.Invalid("project.publish_announcement_invalid", "publication announcement must be at most 500 characters")
	ErrLanguageInvalid     = refusal.Invalid("project.publish_language_invalid", "publication language is not known")
	ErrTooManyPublications = refusal.Invalid("project.publish_too_many", "too many published projects")
)

// reservedSlugs ne nomment pas une page : « api » a été le chemin de l'API
// publique sous /statut, un lien ancien ne doit pas tomber sur une page.
var reservedSlugs = []string{"api"}

// Complete retire les blancs de l'annonce ; le slug se compare tel quel.
func (d PublicationDefinition) Complete() PublicationDefinition {
	d.Slug = strings.TrimSpace(d.Slug)
	d.Announcement = strings.TrimSpace(d.Announcement)
	return d
}

// Validate ne regarde rien d'un projet non publié : sans slug, l'annonce et la
// langue ne servent à rien et ne sont pas gardées.
func (d PublicationDefinition) Validate() error {
	if d.Slug == "" {
		return nil
	}
	if !validSlug(d.Slug) {
		return ErrSlugInvalid
	}
	if utf8.RuneCountInString(d.Announcement) > MaxAnnouncementLength || !utf8.ValidString(d.Announcement) {
		return ErrAnnouncementInvalid
	}
	if d.Language != "" {
		if _, ok := lang.Parse(string(d.Language)); !ok {
			return ErrLanguageInvalid
		}
	}
	return nil
}

// validSlug : minuscules, chiffres et tirets, commençant par une lettre ou
// un chiffre ; un seul segment d'URL, rien à échapper.
func validSlug(slug string) bool {
	if slug == "" || len(slug) > MaxSlugLength || slices.Contains(reservedSlugs, slug) {
		return false
	}
	for index, char := range slug {
		isLower := char >= 'a' && char <= 'z'
		isDigit := char >= '0' && char <= '9'
		if !isLower && !isDigit && (char != '-' || index == 0) {
			return false
		}
	}
	return true
}

// publicationOf rend la publication qui suit la définition : l'instant de la
// première publication est gardé tant que le projet reste publié.
func publicationOf(definition PublicationDefinition, before Publication, now time.Time) Publication {
	if definition.Slug == "" {
		return Publication{}
	}
	publishedAt := before.PublishedAt
	if !before.IsPublished() {
		publishedAt = now
	}
	return Publication{
		Slug: definition.Slug, Announcement: definition.Announcement,
		Language: definition.Language, PublishedAt: publishedAt,
	}
}
