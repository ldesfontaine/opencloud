package status

import (
	"context"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Page rend les réglages de la page, la langue résolue : celle du réglage,
// sinon celle de l'interface.
func (s *Service) Page(ctx context.Context) (Page, error) {
	current, err := s.store.LoadSettings(ctx)
	if err != nil {
		return Page{}, err
	}
	return pageOf(current), nil
}

func pageOf(current settings.Settings) Page {
	language := current.StatusLanguage
	if language == "" {
		language = current.InterfaceLanguage()
	}
	return Page{Title: current.StatusTitle, Announcement: current.StatusAnnouncement, Language: language}
}

// SetPage ne touche que les clés de la page : la langue de l'interface, sur
// la même ligne, est préservée par le composant des réglages.
func (s *Service) SetPage(ctx context.Context, page Page) (Page, error) {
	if err := page.Validate(); err != nil {
		return Page{}, err
	}
	current, err := s.settings.SetStatusPage(ctx, strings.TrimSpace(page.Title), strings.TrimSpace(page.Announcement), page.Language)
	if err != nil {
		return Page{}, err
	}
	s.Refresh(ctx)
	return pageOf(current), nil
}
