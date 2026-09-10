package zone

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// concludedTokenAction écrit une action « Poser le jeton DNS » conclue, avec
// les lignes que le script aurait écrites, puis la rend.
func concludedTokenAction(t *testing.T, database *store.Store, machineID, zone string, lines []string) store.Action {
	t.Helper()

	action := store.Action{
		ID:        "action-" + zone + "-" + machineID,
		MachineID: machineID,
		Kind:      string(catalog.KindDNSToken),
		Params:    map[string]string{"zone": zone},
		State:     store.StatePrepared,
		CreatedAt: time.Now().UTC(),
	}
	if err := database.InsertAction(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if _, err := database.AppendLine(context.Background(), action.ID, time.Now().UTC(), line, ""); err != nil {
			t.Fatal(err)
		}
	}
	action.State = store.StateApplied
	return action
}

func TestActionConcluded_LinksTheFingerprintToTheZoneAndTheMachine(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	placed := Fingerprint(tokenContent(testToken))
	action := concludedTokenAction(t, database, store.LocalMachineID, testZone, []string{
		"étape: poser le jeton de la zone " + testZone,
		"info: jeton=" + placed,
		"résultat: fait",
	})

	keeper.ActionConcluded(context.Background(), action)

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones[0].Machines) != 1 {
		t.Fatalf("machines = %+v, attendu la machine de la pose", zones[0].Machines)
	}
	placement := zones[0].Machines[0]
	if placement.MachineID != store.LocalMachineID || placement.Fingerprint != placed {
		t.Errorf("placement = %+v", placement)
	}
	if !placement.Current {
		t.Error("la machine porte le jeton courant : elle devrait être dite à jour")
	}
}

// Après une rotation, la machine qui n'a pas été rejouée porte encore
// l'ancien : c'est exactement ce que la table doit dire.
func TestActionConcluded_AfterARotation_TheOldFingerprintIsNotCurrent(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	action := concludedTokenAction(t, database, store.LocalMachineID, testZone, []string{
		"info: jeton=" + Fingerprint(tokenContent(testToken)),
		"résultat: fait",
	})
	keeper.ActionConcluded(context.Background(), action)

	if err := keeper.Rotate(context.Background(), testZone, testNewToken); err != nil {
		t.Fatal(err)
	}

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if zones[0].Machines[0].Current {
		t.Error("la machine porte encore l'ancien jeton : elle ne peut pas être dite à jour")
	}
}

// Une action refusée ou échouée ne dit rien de ce que la machine porte.
func TestActionConcluded_ActionThatDidNotApply_WritesNothing(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	action := concludedTokenAction(t, database, store.LocalMachineID, testZone, []string{
		"info: jeton=" + Fingerprint(tokenContent(testToken)),
	})
	action.State = store.StateRefused

	keeper.ActionConcluded(context.Background(), action)

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones[0].Machines) != 0 {
		t.Errorf("machines = %+v, attendu aucune", zones[0].Machines)
	}
}

// Une sortie sans empreinte n'écrit rien : la table dit ce qui a été constaté.
func TestActionConcluded_OutputWithoutAFingerprint_WritesNothing(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	action := concludedTokenAction(t, database, store.LocalMachineID, testZone, []string{
		"info: proxy=running",
		"résultat: fait",
	})

	keeper.ActionConcluded(context.Background(), action)

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones[0].Machines) != 0 {
		t.Errorf("machines = %+v, attendu aucune", zones[0].Machines)
	}
}

// Une ligne de sortie ne décide pas de ce qui entre en base : seule la forme
// que le script écrit est retenue.
func TestActionConcluded_FingerprintThatIsNotOne_IsIgnored(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	action := concludedTokenAction(t, database, store.LocalMachineID, testZone, []string{
		"info: jeton=pas-une-empreinte",
		"résultat: fait",
	})

	keeper.ActionConcluded(context.Background(), action)

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones[0].Machines) != 0 {
		t.Errorf("machines = %+v, attendu aucune", zones[0].Machines)
	}
}
