package status

import (
	"context"
	"sort"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Publications rend les projets publiés par slug, chacun avec sa langue
// résolue. La publication s'écrit par le projet (internal/directory) : le
// statut la lit.
func (s *Service) Publications(ctx context.Context) ([]Publication, error) {
	projects, err := s.reader.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	language, err := s.interfaceLanguage(ctx)
	if err != nil {
		return nil, err
	}
	publications := []Publication{}
	for _, project := range projects {
		if project.Publication.IsPublished() {
			publications = append(publications, publicationOf(project, language))
		}
	}
	sort.Slice(publications, func(i, j int) bool { return publications[i].Slug < publications[j].Slug })
	return publications, nil
}

// PublicationBySlug rend la page à cette adresse ; ErrNotFound sinon.
func (s *Service) PublicationBySlug(ctx context.Context, slug string) (Publication, error) {
	publications, err := s.Publications(ctx)
	if err != nil {
		return Publication{}, err
	}
	for _, publication := range publications {
		if publication.Slug == slug {
			return publication, nil
		}
	}
	return Publication{}, ErrNotFound
}

// PublicationOf rend la page d'un projet ; ErrNotFound s'il n'est pas publié.
func (s *Service) PublicationOf(ctx context.Context, projectID string) (Publication, error) {
	publications, err := s.Publications(ctx)
	if err != nil {
		return Publication{}, err
	}
	for _, publication := range publications {
		if publication.ProjectID == projectID {
			return publication, nil
		}
	}
	return Publication{}, ErrNotFound
}

func publicationOf(project directory.Project, interfaceLanguage lang.Code) Publication {
	title := project.Title
	if title == "" {
		title = project.Name
	}
	language := project.Publication.Language
	if language == "" {
		language = interfaceLanguage
	}
	return Publication{
		ProjectID: project.ID, Slug: project.Publication.Slug, Title: title,
		Announcement: project.Publication.Announcement, Language: language,
	}
}

func (s *Service) interfaceLanguage(ctx context.Context) (lang.Code, error) {
	current, err := s.reader.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	return current.InterfaceLanguage(), nil
}
