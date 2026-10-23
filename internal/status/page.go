package status

import (
	"context"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// CreatePage écrit une page vide : ses composants viennent ensuite, un par
// un. Le slug est son adresse publique, /statut/<slug>.
func (s *Service) CreatePage(ctx context.Context, definition PageDefinition) (Page, error) {
	if err := definition.Validate(); err != nil {
		return Page{}, err
	}
	id, err := ident.New()
	if err != nil {
		return Page{}, fmt.Errorf("generate id: %w", err)
	}
	now := s.now()
	page := pageOf(definition)
	page.ID, page.CreatedAt, page.UpdatedAt = id, now, now
	if err := s.insertPageUnderCeiling(ctx, page); err != nil {
		return Page{}, err
	}
	s.logger.Info("status page created", "page_id", id, "slug", page.Slug)
	s.auditor.Record(ctx, pageEvent(audit.StatusPageCreate, page))
	s.Refresh(ctx)
	return s.GetPage(ctx, id)
}

func (s *Service) insertPageUnderCeiling(ctx context.Context, page Page) error {
	s.creating.Lock()
	defer s.creating.Unlock()
	count, err := s.store.CountPages(ctx)
	if err != nil {
		return err
	}
	if count >= MaxPages {
		return ErrTooManyPages
	}
	return s.store.InsertPage(ctx, page)
}

// UpdatePage réécrit tout ce que l'opérateur règle ; changer le slug change
// l'adresse publique, l'ancienne répond 404.
func (s *Service) UpdatePage(ctx context.Context, id string, definition PageDefinition) (Page, error) {
	if err := definition.Validate(); err != nil {
		return Page{}, err
	}
	current, err := s.store.GetPage(ctx, id)
	if err != nil {
		return Page{}, err
	}
	page := pageOf(definition)
	page.ID, page.CreatedAt, page.UpdatedAt = id, current.CreatedAt, s.now()
	if err := s.store.UpdatePage(ctx, page); err != nil {
		return Page{}, err
	}
	s.auditor.Record(ctx, pageEvent(audit.StatusPageUpdate, page))
	s.Refresh(ctx)
	return s.GetPage(ctx, id)
}

func pageOf(definition PageDefinition) Page {
	return Page{
		ProjectID:    definition.ProjectID,
		Slug:         definition.Slug,
		Title:        strings.TrimSpace(definition.Title),
		Announcement: strings.TrimSpace(definition.Announcement),
		Language:     definition.Language,
	}
}

// DeletePagePlan dit ce que DeletePage ferait : rien ne retient une page ;
// ses composants et leurs membres partent avec elle, les incidents qui les
// citaient restent.
func (s *Service) DeletePagePlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := s.store.GetPage(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return s.store.PageDeletionPlan(ctx, id)
}

// DeletePage retire la page et ses composants ; son adresse répond 404.
func (s *Service) DeletePage(ctx context.Context, id string) error {
	// Relue avant : le journal garde le nom de ce qui part.
	found, err := s.store.GetPage(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeletePage(ctx, id); err != nil {
		return err
	}
	s.logger.Info("status page deleted", "page_id", id, "slug", found.Slug)
	s.auditor.Record(ctx, pageEvent(audit.StatusPageDelete, found))
	s.Refresh(ctx)
	return nil
}

// ListPages rend les pages par slug, chacune avec sa langue résolue.
func (s *Service) ListPages(ctx context.Context) ([]Page, error) {
	pages, err := s.store.ListPages(ctx)
	if err != nil {
		return nil, err
	}
	language, err := s.interfaceLanguage(ctx)
	if err != nil {
		return nil, err
	}
	for i := range pages {
		pages[i].Language = resolvedLanguage(pages[i].Language, language)
	}
	return pages, nil
}

func (s *Service) GetPage(ctx context.Context, id string) (Page, error) {
	page, err := s.store.GetPage(ctx, id)
	if err != nil {
		return Page{}, err
	}
	return s.withLanguage(ctx, page)
}

// PageBySlug rend la page à cette adresse ; ErrNotFound sinon.
func (s *Service) PageBySlug(ctx context.Context, slug string) (Page, error) {
	page, err := s.store.GetPageBySlug(ctx, slug)
	if err != nil {
		return Page{}, err
	}
	return s.withLanguage(ctx, page)
}

// withLanguage donne à la page la langue de l'interface si elle n'en a pas.
func (s *Service) withLanguage(ctx context.Context, page Page) (Page, error) {
	language, err := s.interfaceLanguage(ctx)
	if err != nil {
		return Page{}, err
	}
	page.Language = resolvedLanguage(page.Language, language)
	return page, nil
}

func (s *Service) interfaceLanguage(ctx context.Context) (lang.Code, error) {
	current, err := s.reader.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	return current.InterfaceLanguage(), nil
}

func resolvedLanguage(page, interfaceLanguage lang.Code) lang.Code {
	if page == "" {
		return interfaceLanguage
	}
	return page
}
