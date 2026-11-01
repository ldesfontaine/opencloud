package directory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Publier un projet pose sa page à /statut/<slug> ; la dépublier la retire.
// L'instant de la première publication se garde tant que le projet reste
// publié, et l'annonce de la page ne survit pas à la dépublication.
func TestProjects_PublishRepublishAndUnpublish(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project := mustCreateProject(t, b, "client-a")
	if project.Publication.IsPublished() {
		t.Fatalf("un projet neuf n'est pas publié : %+v", project.Publication)
	}

	published, err := b.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{
		Name: "client-a", Publication: directory.PublicationDefinition{Slug: " client-a ", Announcement: " Coupure samedi. ", Language: lang.English},
	})
	if err != nil {
		t.Fatal(err)
	}
	publication := published.Publication
	if publication.Slug != "client-a" || publication.Announcement != "Coupure samedi." || publication.Language != lang.English || !publication.PublishedAt.Equal(testNow) {
		t.Fatalf("publication %+v", publication)
	}

	later := testNow.Add(24 * time.Hour)
	b.directory.SetClock(func() time.Time { return later })
	rewritten, err := b.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{
		Name: "client-a", Publication: directory.PublicationDefinition{Slug: "client-a2"},
	})
	if err != nil || !rewritten.Publication.PublishedAt.Equal(testNow) || rewritten.Publication.Announcement != "" {
		t.Fatalf("l'instant de première publication est gardé : %+v %v", rewritten.Publication, err)
	}

	unpublished, err := b.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{
		Name: "client-a", Publication: directory.PublicationDefinition{Announcement: "perdue", Language: lang.English},
	})
	if err != nil || unpublished.Publication != (directory.Publication{}) {
		t.Fatalf("sans slug, rien n'est publié ni gardé : %+v %v", unpublished.Publication, err)
	}
	again, err := b.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{
		Name: "client-a", Publication: directory.PublicationDefinition{Slug: "client-a"},
	})
	if err != nil || !again.Publication.PublishedAt.Equal(later) {
		t.Fatalf("republier compte une nouvelle publication : %+v %v", again.Publication, err)
	}
}

func TestProjects_PublicationRefusesWhatMakesNoSense(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	first := mustCreateProject(t, b, "client-a")
	second := mustCreateProject(t, b, "client-b")
	if _, err := b.directory.UpdateProject(ctx, first.ID, directory.ProjectDefinition{Name: "client-a", Publication: directory.PublicationDefinition{Slug: "statut"}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		publication directory.PublicationDefinition
		want        error
	}{
		{"slug en majuscules", directory.PublicationDefinition{Slug: "Client"}, directory.ErrSlugInvalid},
		{"slug qui commence par un tiret", directory.PublicationDefinition{Slug: "-a"}, directory.ErrSlugInvalid},
		{"slug avec une barre", directory.PublicationDefinition{Slug: "a/b"}, directory.ErrSlugInvalid},
		{"slug réservé", directory.PublicationDefinition{Slug: "api"}, directory.ErrSlugInvalid},
		{"slug trop long", directory.PublicationDefinition{Slug: strings.Repeat("a", directory.MaxSlugLength+1)}, directory.ErrSlugInvalid},
		{"slug pris par un autre projet", directory.PublicationDefinition{Slug: "statut"}, directory.ErrSlugTaken},
		{"langue inconnue", directory.PublicationDefinition{Slug: "b", Language: "de"}, directory.ErrLanguageInvalid},
		{"annonce trop longue", directory.PublicationDefinition{Slug: "b", Announcement: strings.Repeat("a", directory.MaxAnnouncementLength+1)}, directory.ErrAnnouncementInvalid},
	}
	for _, tc := range cases {
		definition := directory.ProjectDefinition{Name: "client-b", Publication: tc.publication}
		if _, err := b.directory.UpdateProject(ctx, second.ID, definition); !errors.Is(err, tc.want) {
			t.Errorf("%s : %v, attendu %v", tc.name, err, tc.want)
		}
		definition.Name = "client-c"
		if _, err := b.directory.CreateProject(ctx, definition); !errors.Is(err, tc.want) {
			t.Errorf("%s à la création : %v, attendu %v", tc.name, err, tc.want)
		}
	}
}

// Supprimer un projet publié est permis : la page part avec lui, et le plan
// le dit avant le geste.
func TestDeleteProject_APublishedProjectGoesWithItsPage(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	project, err := b.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "client-a", Publication: directory.PublicationDefinition{Slug: "client-a"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := b.directory.DeleteProjectPlan(ctx, project.ID)
	if err != nil || plan.IsBlocked() || len(plan.Removes) == 0 || plan.Removes[0].Kind != "publications" || plan.Removes[0].Count != 1 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := b.directory.DeleteProject(ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.GetProject(ctx, project.ID); !errors.Is(err, directory.ErrProjectNotFound) {
		t.Fatalf("le projet existe encore : %v", err)
	}
}
