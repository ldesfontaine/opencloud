package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// reportTarget fait rapporter à la cible l'état de son rest-server.
func (b *resticBench) reportTarget(t *testing.T, report agentwire.ResticTargetReport) {
	t.Helper()
	section := agentwire.ResticReport{Target: &report}
	if got := signalAnnouncingSections(b.testServer, b.target.Token, agentwire.SignalRequest{Restic: &section}); got.Code != http.StatusNoContent {
		t.Fatalf("target report: %d %s", got.Code, got.Body.String())
	}
}

// runningTarget : la cible en marche, avec ce certificat et ces utilisateurs.
func runningTarget(certificate, fingerprint string, users ...string) agentwire.ResticTargetReport {
	return agentwire.ResticTargetReport{
		State: agentwire.ResticTargetRunning, Host: resticTargetHost, Port: resticTargetPort, Certificate: certificate,
		Fingerprint: fingerprint, Users: users, RetentionFloor: &agentwire.ResticRetentionFloor{KeepDaily: 7},
	}
}

// La cible a refait son certificat (ses noms ont changé) : chaque source
// liée reçoit de nouveau restic.provision, avec les mêmes mots de passe, lus
// en base.
func TestRestic_ANewCertificateProvisionsEachSourceAgain(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	source := tapOrders(t, bench.source)
	certificate, fingerprint := testCertificate(t)
	bench.reportTarget(t, runningTarget(certificate, fingerprint, resticSourceID))

	provision := source.next(t, agentwire.KindResticProvision)
	var params agentwire.ResticProvisionParams
	var secret agentwire.ResticProvisionSecret
	if json.Unmarshal(provision.Params, &params) != nil || params.Certificate != certificate {
		t.Fatalf("provision %s", provision.Params)
	}
	if json.Unmarshal(provision.Secret, &secret) != nil || secret != bench.provisioned {
		t.Fatal("the passwords changed with the certificate")
	}
	// Le même rapport une seconde fois : rien ne repart.
	bench.reportTarget(t, runningTarget(certificate, fingerprint, resticSourceID))
	source.none(t, "the same certificate again")
}

// Le rapport de la cible ne cite plus l'utilisateur d'un lien (un fichier
// htpasswd refait) : restic.user.add repart, avec le même mot de passe REST ;
// un utilisateur que plus aucun lien ne tient est fermé.
func TestRestic_AUserMissingFromTheTargetIsOpenedAgain(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)
	stored, err := bench.db.GetResticTarget(context.Background(), resticTargetID)
	if err != nil {
		t.Fatal(err)
	}
	bench.reportTarget(t, runningTarget(stored.Reported.Certificate, stored.Reported.Fingerprint, "ancienne-source"))

	orders := map[agentwire.Kind]agentwire.OrderBody{}
	for range 2 {
		order := <-target.orders
		orders[order.Kind] = order
	}
	var secret agentwire.ResticUserSecret
	added, removed := orders[agentwire.KindResticUserAdd], orders[agentwire.KindResticUserRemove]
	if json.Unmarshal(added.Secret, &secret) != nil || secret.RESTPassword != bench.provisioned.RESTPassword {
		t.Fatalf("user add %s", added.Params)
	}
	var closed agentwire.ResticUserRemoveParams
	if json.Unmarshal(removed.Params, &closed) != nil || closed.User != "ancienne-source" || len(removed.Secret) != 0 {
		t.Fatalf("user remove %s", removed.Params)
	}
	bench.reportTarget(t, runningTarget(stored.Reported.Certificate, stored.Reported.Fingerprint, resticSourceID))
	target.none(t, "the user is back")
}

// La source a perdu son lien (dossier d'état refait) : son rapport ne cite
// plus la cible, restic.provision repart.
func TestRestic_ASourceThatLostItsLinkIsProvisionedAgain(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	source := tapOrders(t, bench.source)
	report := agentwire.ResticReport{Source: &agentwire.ResticSourceReport{Version: "restic 0.18.1"}}
	if got := signalAnnouncingSections(bench.testServer, bench.source.Token, agentwire.SignalRequest{Restic: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("source report: %d %s", got.Code, got.Body.String())
	}
	provision := source.next(t, agentwire.KindResticProvision)
	var secret agentwire.ResticProvisionSecret
	if json.Unmarshal(provision.Secret, &secret) != nil || secret != bench.provisioned {
		t.Fatal("the passwords changed")
	}
	report.Source.Targets = []string{resticTargetID}
	if got := signalAnnouncingSections(bench.testServer, bench.source.Token, agentwire.SignalRequest{Restic: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("source report: %d %s", got.Code, got.Body.String())
	}
	source.none(t, "the link is back")
}

// Supprimer la dernière sauvegarde d'une source vers une cible ferme le
// dépôt (restic.user.remove, sans secret) et retire le lien, ses mots de
// passe gardés scellés ; une autre sauvegarde du même lien n'y touche pas.
// Redéclaré, le couple rouvre le même dépôt avec les mêmes mots de passe.
func TestRestic_TheLastBackupClosesTheRepositoryAndARedeclarationReopensIt(t *testing.T) {
	bench := readyResticBench(t)
	first := bench.createServiceBackup(t)
	other := e2eComposeService(t, bench.testServer, resticSourceID, "photos")
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "photos", "scope": "service", "service_id": other, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("second: %d %s", got.Code, got.Body.String())
	}
	var second resticBackupCreatedJSON
	decodeAPI(t, got, &second)
	var secret agentwire.ResticProvisionSecret
	if provision := nextOrder(t, bench.source); json.Unmarshal(provision.Secret, &secret) != nil || secret != bench.provisioned {
		t.Fatal("a second backup of the same link draws new passwords")
	}
	nextOrder(t, bench.target)
	target := tapOrders(t, bench.target)

	if got := callAPI(bench.Server, http.MethodDelete, "/api/machines/"+resticSourceID+"/restic-backups/"+first, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete first: %d %s", got.Code, got.Body.String())
	}
	target.none(t, "a backup remains on the link")
	if _, err := bench.db.GetResticLink(context.Background(), resticSourceID, resticTargetID); err != nil {
		t.Fatalf("the link is gone with a backup left: %v", err)
	}

	if got := callAPI(bench.Server, http.MethodDelete, "/api/machines/"+resticSourceID+"/restic-backups/"+second.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("delete second: %d %s", got.Code, got.Body.String())
	}
	removed := target.next(t, agentwire.KindResticUserRemove)
	var params agentwire.ResticUserRemoveParams
	if json.Unmarshal(removed.Params, &params) != nil || params.User != resticSourceID || len(removed.Secret) != 0 {
		t.Fatalf("user remove %s", removed.Params)
	}
	retired, err := bench.db.GetResticLink(context.Background(), resticSourceID, resticTargetID)
	if err != nil || !retired.IsRetired() || retired.Secrets.REST != bench.provisioned.RESTPassword || retired.Secrets.Repo != bench.provisioned.RepoPassword {
		t.Fatalf("the retired link must keep its sealed passwords: %v", err)
	}
	if listed, err := bench.db.ListResticLinks(context.Background()); err != nil || len(listed) != 0 {
		t.Fatalf("a retired link is still in service: %d %v", len(listed), err)
	}
	if overview := callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(); strings.Contains(overview, retired.ID) {
		t.Fatal("a retired link shows in the overview")
	}

	// Redéclarée : les mêmes mots de passe repartent, le dépôt existant se rouvre.
	source := tapOrders(t, bench.source)
	again := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": bench.serviceID, "target_machine_id": resticTargetID,
	})
	if again.Code != http.StatusCreated {
		t.Fatalf("redeclare: %d %s", again.Code, again.Body.String())
	}
	var reopened agentwire.ResticProvisionSecret
	if provision := source.next(t, agentwire.KindResticProvision); json.Unmarshal(provision.Secret, &reopened) != nil || reopened != bench.provisioned {
		t.Fatal("a redeclaration draws new passwords: the existing repository would not open")
	}
	var userSecret agentwire.ResticUserSecret
	if userAdd := target.next(t, agentwire.KindResticUserAdd); json.Unmarshal(userAdd.Secret, &userSecret) != nil || userSecret.RESTPassword != bench.provisioned.RESTPassword {
		t.Fatal("the target is not reopened with the same REST password")
	}
	link, err := bench.db.GetResticLink(context.Background(), resticSourceID, resticTargetID)
	if err != nil || link.IsRetired() || link.ID != retired.ID {
		t.Fatalf("the link is not back in service: %v", err)
	}
}

// Sans restic sur la source, ou sans rest-server sur la cible, la création
// est refusée avec ce qu'il faut installer ; l'aperçu le dit machine par
// machine, avec le système et l'architecture enrôlés.
func TestRestic_AMissingBinaryIsRefusedWithWhatToInstall(t *testing.T) {
	bench := newResticBench(t, "observe,restic.produce", "observe,restic.target=/srv/restic")
	ctx := context.Background()
	sourceMachine, err := bench.machines.Get(ctx, resticSourceID)
	if err != nil {
		t.Fatal(err)
	}
	bench.machines.Disconnect(bench.source)
	if _, err := bench.machines.Connect(ctx, sourceMachine.Machine, "192.0.2.21", "v0.0.1", "observe,restic.produce", "", "restic.produce=restic_missing"); err != nil {
		t.Fatal(err)
	}
	setup := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup",
		map[string]any{"host": resticTargetHost, "port": resticTargetPort})
	if setup.Code != http.StatusNoContent {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	bench.reportTarget(t, agentwire.ResticTargetReport{State: agentwire.ResticTargetError, Reason: agentwire.ResticErrorBinaryMissing})
	request := map[string]any{"name": "nextcloud", "scope": "service", "service_id": bench.serviceID, "target_machine_id": resticTargetID}
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", request)
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.rest_server_missing" {
		t.Fatalf("target without rest-server: %d %s", got.Code, got.Body.String())
	}
	certificate, fingerprint := testCertificate(t)
	bench.reportTarget(t, runningTarget(certificate, fingerprint))
	got = callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", request)
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.restic_missing" {
		t.Fatalf("source without restic: %d %s", got.Code, got.Body.String())
	}
	for _, fit := range bench.overview(t).Machines {
		if fit.MachineID == resticSourceID && (fit.ResticAvailable || fit.Arch == "" || fit.OS == "") {
			t.Fatalf("source %+v", fit)
		}
	}
}
