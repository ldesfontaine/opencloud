package manifest_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

// undeclaredGroup rend le groupe non déclaré de l'écart pour cette clé.
func (b *bench) undeclaredGroup(t *testing.T, kind directory.IgnoreKind, value string) (directory.Undeclared, bool) {
	t.Helper()
	report, err := b.directory.Gaps(context.Background(), directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range report.Undeclared {
		if group.Key.Kind == kind && group.Key.Value == value {
			return group, true
		}
	}
	return directory.Undeclared{}, false
}

// Un fichier qui ne liste pas une clé ignorée ne la dé-ignore jamais sans
// -prune. Avec -prune, le plan nomme la clé AVANT l'application ; ce n'est
// qu'à l'application que la proposition revient.
func TestPrune_UnlistedIgnoredKeyIsNamedInThePlanBeforeTheProposalComesBack(t *testing.T) {
	b, data := seeded(t)
	b.seedUndeclared(t)
	data = replaceOnce(t, data, "  - machine: opencloud\n    container_name: portainer\n", "")
	const named = "delete ignored/opencloud/container_name:portainer"

	group, found := b.undeclaredGroup(t, directory.IgnoreContainerName, "portainer")
	if !found || !group.Ignored {
		t.Fatalf("portainer starts ignored: %+v", group)
	}

	// Sans -prune : gardée, dite « hors fichier », rien n'est écrit.
	plan := b.plan(t, data, false)
	if slices.Contains(changeLabels(plan), named) {
		t.Fatalf("no prune, no deletion: %v", changeLabels(plan))
	}
	if !slices.ContainsFunc(plan.Unmanaged, func(ref manifest.Ref) bool { return ref.Kind == manifest.KindIgnored && ref.Name == "opencloud/container_name:portainer" }) {
		t.Fatalf("the key is said outside the file: %+v", plan.Unmanaged)
	}
	if _, _, err := b.tryApply(data, false); err != manifest.ErrNothingToApply {
		t.Fatalf("apply without prune: %v", err)
	}
	if group, _ := b.undeclaredGroup(t, directory.IgnoreContainerName, "portainer"); !group.Ignored {
		t.Fatal("the key stopped being ignored without -prune")
	}

	// Avec -prune : le plan la nomme, puis l'application la retire.
	plan = b.plan(t, data, true)
	if !slices.Contains(changeLabels(plan), named) {
		t.Fatalf("the plan must name the key: %v", changeLabels(plan))
	}
	if group, _ := b.undeclaredGroup(t, directory.IgnoreContainerName, "portainer"); !group.Ignored {
		t.Fatal("planning must write nothing")
	}
	b.apply(t, data, true)
	group, found = b.undeclaredGroup(t, directory.IgnoreContainerName, "portainer")
	if !found || group.Ignored || !group.Adoptable {
		t.Fatalf("the proposal comes back after the pruned key: %+v", group)
	}
}

// Un service accepté est un service comme les autres : un fichier qui ne le
// liste pas le garde sans -prune, le supprime avec -prune en le nommant au
// plan. Ses conteneurs tournent encore : il redevient une proposition, jamais
// un service.
func TestPrune_AcceptedServiceNotInTheFileIsNamedDeletedAndComesBackAsAProposal(t *testing.T) {
	b, data := seeded(t)
	b.seedUndeclared(t)
	ctx := context.Background()

	proposal, err := b.directory.Propose(ctx, container.ID(remoteID, dockerID("1")))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := b.directory.CreateService(ctx, proposal.Definition)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.DeclaredBy != directory.ByOperator || accepted.Name != "blog" {
		t.Fatalf("accepted: %+v", accepted)
	}
	if _, found := b.undeclaredGroup(t, directory.IgnoreComposeProject, "blog"); found {
		t.Fatal("an accepted service is no longer a proposal")
	}

	// Le fichier a été écrit avant l'acceptation : il ne liste pas « blog ».
	plan := b.plan(t, data, false)
	if len(plan.Changes) > 0 || !slices.ContainsFunc(plan.Unmanaged, func(ref manifest.Ref) bool { return ref.Kind == manifest.KindService && ref.Name == "app-1/blog" }) {
		t.Fatalf("without prune the accepted service is kept and named: %v / %+v", changeLabels(plan), plan.Unmanaged)
	}
	if _, found := b.undeclaredGroup(t, directory.IgnoreComposeProject, "blog"); found {
		t.Fatal("planning without prune moved the proposal")
	}

	plan = b.plan(t, data, true)
	if !slices.Contains(changeLabels(plan), "delete service/app-1/blog") {
		t.Fatalf("the plan must name the accepted service: %v", changeLabels(plan))
	}
	b.apply(t, data, true)

	group, found := b.undeclaredGroup(t, directory.IgnoreComposeProject, "blog")
	if !found || group.Ignored || !group.Adoptable {
		t.Fatalf("the containers still run: the proposal is back: %+v", group)
	}
	services, err := b.db.ListDeclaredServices(ctx, directory.Filter{MachineID: remoteID})
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if service.Name == "blog" {
			t.Fatal("the pruned service came back as a service")
		}
	}
}
