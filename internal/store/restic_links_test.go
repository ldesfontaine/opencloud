package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

const (
	plainRESTPassword = "rest-password-drawn-by-the-server-0123456789" // gitleaks:allow -- valeur de test
	plainRepoPassword = "repo-password-drawn-by-the-server-0123456789" // gitleaks:allow -- valeur de test
)

func seedResticLinkMachines(t *testing.T, db *DB) {
	t.Helper()
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES ('vps', 'vps', 'vps', 1), ('nas', 'nas', 'nas', 1), ('pi', 'pi', 'pi', 1)`)
	exec(t, db, `INSERT INTO restic_targets (machine_id, requested_host, requested_port, requested_dir, requested_at) VALUES ('nas', '192.0.2.10', 8000, '/srv/restic', 1)`)
}

// Les mots de passe d'un lien sont scellés, liés à leur ligne et à leur
// colonne, ouverts seulement par GetResticLink ; une seconde pose garde les
// premiers, retiré ou non ; la rotation les rescelle, la clé perdue les vide.
func TestResticLink_ThePasswordsAreSealedBoundAndForgotten(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	seedResticLinkMachines(t, db)
	first := restic.Link{ID: "lnk1", SourceMachineID: "vps", TargetMachineID: "nas",
		Secrets: restic.LinkSecrets{REST: plainRESTPassword, Repo: plainRepoPassword}, CreatedAt: time.Unix(10, 0)}
	link, isNew, err := db.EnsureResticLink(ctx, first)
	if err != nil || !isNew || link.Secrets != first.Secrets {
		t.Fatalf("first link: %v %v", isNew, err)
	}
	for _, column := range []string{"rest_password", "repo_password"} {
		stored := rawColumn(t, db, `SELECT `+column+` FROM restic_links`)
		if !seal.IsSealed(stored) || stored == plainRESTPassword || stored == plainRepoPassword {
			t.Fatalf("%s is not sealed", column)
		}
	}
	again := first
	again.ID, again.Secrets = "lnk2", restic.LinkSecrets{REST: "other-rest-password-0123456789-abcdef", Repo: "other-repo-password-0123456789-abcdef"}
	link, isNew, err = db.EnsureResticLink(ctx, again)
	if err != nil || isNew || link.ID != "lnk1" || link.Secrets != first.Secrets {
		t.Fatalf("a second link for the same pair: %v %v %s", isNew, err, link.ID)
	}

	// Recopiés sur une autre ligne, ou d'une colonne à l'autre, ils ne s'ouvrent pas.
	sealedREST := rawColumn(t, db, `SELECT rest_password FROM restic_links`)
	exec(t, db, `INSERT INTO restic_links (id, source_machine_id, target_machine_id, rest_password, repo_password, created_at)
		VALUES ('copy', 'pi', 'nas', ?, ?, 1)`, sealedREST, sealedREST)
	if _, err := db.GetResticLink(ctx, "pi", "nas"); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("a sealed password copied to another row: %v", err)
	}
	exec(t, db, `DELETE FROM restic_links WHERE id = 'copy'`)

	listed, err := db.ListResticLinks(ctx)
	if err != nil || len(listed) != 1 || listed[0].Secrets != (restic.LinkSecrets{}) {
		t.Fatalf("a listed link carries its passwords: %v", err)
	}
	outcome := restic.RetainOutcome{RunID: "run1", State: restic.StateSucceeded, At: time.Unix(20, 0)}
	if err := db.NoteResticRetain(ctx, "lnk1", outcome); err != nil {
		t.Fatal(err)
	}
	link, err = db.GetResticLink(ctx, "vps", "nas")
	if err != nil || link.Retain != outcome {
		t.Fatalf("retain %+v %v", link.Retain, err)
	}

	// Retiré, le lien sort de la liste mais garde ses mots de passe ; remis en
	// service, il revient tel quel.
	if err := db.SetResticLinkRetired(ctx, "lnk1", time.Unix(30, 0)); err != nil {
		t.Fatal(err)
	}
	if listed, err := db.ListResticLinks(ctx); err != nil || len(listed) != 0 {
		t.Fatalf("a retired link is listed: %d %v", len(listed), err)
	}
	link, err = db.GetResticLink(ctx, "vps", "nas")
	if err != nil || !link.IsRetired() || link.Secrets != first.Secrets {
		t.Fatalf("retired: %v", err)
	}
	if _, isNew, err := db.EnsureResticLink(ctx, again); err != nil || isNew {
		t.Fatalf("a retired pair draws a new link: %v %v", isNew, err)
	}

	if resealed, err := db.ResealSecrets(ctx); err != nil || resealed != 2 {
		t.Fatalf("reseal: %d %v", resealed, err)
	}
	forgotten, err := db.ForgetSecrets(ctx, func() (string, error) { return "renewed", nil })
	if err != nil || forgotten.ResticLinks != 1 {
		t.Fatalf("forgotten: %+v %v", forgotten, err)
	}
	link, err = db.GetResticLink(ctx, "vps", "nas")
	if err != nil || link.Secrets != (restic.LinkSecrets{}) {
		t.Fatalf("forgotten passwords must read empty: %v", err)
	}
	if err := db.DeleteResticLink(ctx, "vps", "nas"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteResticLink(ctx, "vps", "nas"); !errors.Is(err, restic.ErrLinkNotFound) {
		t.Fatalf("second removal: %v", err)
	}
}
