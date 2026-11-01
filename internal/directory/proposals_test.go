package directory_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

var (
	blogWebID = container.ID(remoteID, containerID1)
	blogDBID  = container.ID(remoteID, containerID2)
)

func recordBlog(t *testing.T, b *bench, webID, dbID string) {
	t.Helper()
	report := inventoryOf(
		composeContainerOf(webID, "blog-web-1", "blog", "web", "nginx:1.27"),
		composeContainerOf(dbID, "blog-db-1", "blog", "db", "postgres:16.4"),
	)
	if err := b.containers.Record(context.Background(), remoteID, report); err != nil {
		t.Fatal(err)
	}
}

func undeclaredKeys(t *testing.T, b *bench) map[directory.IgnoredKey]bool {
	t.Helper()
	report, err := b.directory.Gaps(context.Background(), directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[directory.IgnoredKey]bool{}
	for _, group := range report.Undeclared {
		keys[directory.IgnoredKey{MachineID: group.MachineID, Kind: group.Key.Kind, Value: group.Key.Value}] = group.Ignored
	}
	return keys
}

var blogKey = directory.IgnoredKey{MachineID: remoteID, Kind: directory.IgnoreComposeProject, Value: "blog"}

// Une proposition ignorée ne revient pas : ni dans la liste proposée, ni par
// son conteneur, même recréé sous un autre identifiant Docker. La clé suit le
// projet Compose, pas le conteneur. Ne plus l'ignorer la rend.
func TestProposal_IgnoredOneNeverComesBack(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	recordBlog(t, b, containerID1, containerID2)
	if err := b.directory.Ignore(ctx, blogKey); err != nil {
		t.Fatal(err)
	}

	// Les conteneurs sont recréés : autres identifiants, même projet.
	recreatedWeb, recreatedDB := strings.Repeat("7", 64), strings.Repeat("8", 64)
	recordBlog(t, b, recreatedWeb, recreatedDB)

	proposals, err := b.directory.Proposals(ctx)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("an ignored group is not proposed: %+v %v", proposals, err)
	}
	if _, err := b.directory.Propose(ctx, container.ID(remoteID, recreatedWeb)); !errors.Is(err, directory.ErrContainerIgnored) || !refusal.IsConflict(err) {
		t.Fatalf("propose the recreated container: %v", err)
	}
	if ignored, shown := undeclaredKeys(t, b)[blogKey]; !shown || !ignored {
		t.Fatal("the ignored group stays listed as ignored, so it can be restored")
	}

	if err := b.directory.Unignore(ctx, blogKey); err != nil {
		t.Fatal(err)
	}
	proposal, err := b.directory.Propose(ctx, container.ID(remoteID, recreatedWeb))
	if err != nil || proposal.Definition.ComposeProject != "blog" {
		t.Fatalf("no longer ignored, the proposal is there: %+v %v", proposal, err)
	}
}

// Accepter deux fois (deux onglets, un double clic) ne fait pas deux
// services : le second reçoit un refus d'état, un conflit.
func TestProposal_AcceptedTwiceMakesOneService(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	recordBlog(t, b, containerID1, containerID2)
	proposal, err := b.directory.Propose(ctx, blogWebID)
	if err != nil {
		t.Fatal(err)
	}

	first, err := b.directory.CreateService(ctx, proposal.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeclaredBy != directory.ByOperator {
		t.Fatalf("declared by %q: the operator accepted it", first.DeclaredBy)
	}
	_, err = b.directory.CreateService(ctx, proposal.Definition)
	if !errors.Is(err, directory.ErrNameTaken) || !refusal.IsConflict(err) {
		t.Fatalf("second acceptance: %v", err)
	}
	// Même nom changé à la main : le projet Compose, lui, est déjà pris.
	renamed := proposal.Definition
	renamed.Name = "blog-bis"
	if _, err := b.directory.CreateService(ctx, renamed); !errors.Is(err, directory.ErrComposeProjectTaken) || !refusal.IsConflict(err) {
		t.Fatalf("same compose project: %v", err)
	}
	services, err := b.db.ListDeclaredServices(ctx, directory.Filter{MachineID: remoteID})
	if err != nil || len(services) != 1 {
		t.Fatalf("one service, got %d: %v", len(services), err)
	}
	// Le second onglet qui relit la proposition apprend que c'est fait.
	if _, err := b.directory.Propose(ctx, blogWebID); !errors.Is(err, directory.ErrContainerDeclared) {
		t.Fatalf("proposal of a declared container: %v", err)
	}
}

// Supprimer un service accepté depuis l'interface : ses conteneurs tournent,
// il redevient une proposition. « Supprimer et ignorer » le fait en un geste
// et ne laisse pas de proposition.
func TestProposal_DeletedServiceComesBackUnlessIgnoredWithIt(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	recordBlog(t, b, containerID1, containerID2)

	accept := func(id string) directory.Service {
		t.Helper()
		proposal, err := b.directory.Propose(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return mustCreate(t, b, proposal.Definition)
	}

	service := accept(blogWebID)
	if _, listed := undeclaredKeys(t, b)[blogKey]; listed {
		t.Fatal("an accepted service is not a proposal")
	}
	if err := b.directory.DeleteService(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	if ignored, listed := undeclaredKeys(t, b)[blogKey]; !listed || ignored {
		t.Fatalf("deleted, it is a proposal again: listed=%v ignored=%v", listed, ignored)
	}

	service = accept(blogWebID)
	if err := b.directory.DeleteServiceAndIgnore(ctx, service.ID); err != nil {
		t.Fatal(err)
	}
	if ignored, listed := undeclaredKeys(t, b)[blogKey]; !listed || !ignored {
		t.Fatalf("deleted and ignored, no proposal: listed=%v ignored=%v", listed, ignored)
	}
	proposals, err := b.directory.Proposals(ctx)
	if err != nil || len(proposals) != 0 {
		t.Fatalf("proposals: %+v %v", proposals, err)
	}
}

// Un service container laisse une clé par conteneur ; un binaire n'en laisse
// aucune.
func TestDeleteServiceAndIgnore_KeysFollowTheOrigin(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	definition := containerDefinition(remoteID, "whoami", "whoami")
	definition.Units = append(definition.Units, directory.UnitDefinition{Name: "sidecar", Image: "busybox", ContainerName: "whoami-sidecar"})
	container := mustCreate(t, b, definition)
	binary := mustCreate(t, b, directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "main", SystemdUnit: "caddy.service"}},
	})

	if err := b.directory.DeleteServiceAndIgnore(ctx, binary.ID); err != nil {
		t.Fatal(err)
	}
	if ignored, err := b.db.ListIgnoredKeys(ctx, ""); err != nil || len(ignored) != 0 {
		t.Fatalf("a binary leaves nothing to ignore: %+v %v", ignored, err)
	}
	if err := b.directory.DeleteServiceAndIgnore(ctx, container.ID); err != nil {
		t.Fatal(err)
	}
	ignored, err := b.db.ListIgnoredKeys(ctx, "")
	if err != nil || len(ignored) != 2 {
		t.Fatalf("one key per container: %+v %v", ignored, err)
	}
	for _, key := range ignored {
		if key.Kind != directory.IgnoreContainerName || (key.Value != "whoami" && key.Value != "whoami-sidecar") {
			t.Fatalf("key %+v", key)
		}
	}
	if err := b.directory.DeleteServiceAndIgnore(ctx, "svc_inconnu"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("unknown service: %v", err)
	}
}

// Un service que des sondes retiennent n'est pas supprimé, donc rien n'est
// ignoré non plus.
func TestDeleteServiceAndIgnore_RefusedDeletionIgnoresNothing(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	created := mustCreate(t, b, composeDefinition(remoteID))
	watch(t, b, created.ID)

	err := b.directory.DeleteServiceAndIgnore(ctx, created.ID)

	var refused *deletion.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("want the deletion refusal, got %v", err)
	}
	if ignored, err := b.db.ListIgnoredKeys(ctx, ""); err != nil || len(ignored) != 0 {
		t.Fatalf("nothing ignored after a refusal: %+v %v", ignored, err)
	}
}

// L'agent ne déclare rien : faire varier les conteneurs (ils apparaissent,
// changent de projet, sont recréés, disparaissent, portent des labels) puis
// lire l'écart, les propositions et les fiches n'écrit ni service, ni projet,
// ni clé ignorée, et n'annonce aucun changement de l'annuaire.
func TestProposal_NothingIsWrittenWithoutAGesture(t *testing.T) {
	b := newBench(t)
	ctx := context.Background()
	mustCreate(t, b, containerDefinition(remoteID, "whoami", "whoami"))
	before := snapshotOf(t, b)
	changesBefore := b.changeCount()

	reports := []container.Report{
		inventoryOf(composeContainerOf(containerID1, "blog-web-1", "blog", "web", "nginx:1.27")),
		inventoryOf(
			composeContainerOf(containerID1, "blog-web-1", "blog", "web", "nginx:1.27"),
			composeContainerOf(containerID2, "blog-db-1", "blog", "db", "postgres:16.4"),
			composeContainerOf(strings.Repeat("3", 64), "tmp-debug", "", "", "busybox"),
		),
		inventoryOf(composeContainerOf(strings.Repeat("4", 64), "blog-web-1", "blog-v2", "web", "nginx:1.28")),
		inventoryOf(withLabels(composeContainerOf(strings.Repeat("5", 64), "stray", "", "", "alpine"), "svc_inconnu", "main")),
		inventoryOf(),
	}
	for _, report := range reports {
		if err := b.containers.Record(ctx, remoteID, report); err != nil {
			t.Fatal(err)
		}
		if _, err := b.directory.Gaps(ctx, directory.Filter{}); err != nil {
			t.Fatal(err)
		}
		if _, err := b.directory.Proposals(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{blogWebID, blogDBID} {
			_, _ = b.directory.Propose(ctx, id)
		}
	}

	if after := snapshotOf(t, b); !reflect.DeepEqual(before, after) {
		t.Fatalf("the directory moved without a gesture:\nbefore %+v\nafter  %+v", before, after)
	}
	if b.changeCount() != changesBefore {
		t.Fatal("the directory announced a change nobody made")
	}
}
