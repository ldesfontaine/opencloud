package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Le banc de non-fuite : le mot de passe REST et le mot de passe du dépôt,
// semés dans le secret des ordres comme le serveur les envoie, et la clé
// privée du certificat de la cible sont cherchés dans tout ce qui sort de
// l'agent : le rapport de signal sérialisé, les résultats d'ordres (lignes
// de sortie, bilan, raisons), le journal (erreurs comprises), les arguments
// et l'environnement des processus lancés, l'événement de relais et ce que
// la cible rend au serveur. Le faux restic essaie de les écrire sur sa
// sortie d'erreur, comme le ferait une erreur d'URL ou un producteur bavard.
func TestResticAgent_NeverLeaksSecrets(t *testing.T) {
	b := newTargetBench(t, "restic.produce")
	var outputs []string
	keep := func(results []agentwire.ActionResult) agentwire.ActionResult {
		encoded, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(encoded))
		return finalOf(t, results)
	}

	// La cible : certificat et clé tirés, une source ouverte avec son mot de
	// passe REST, rest-server tenu ici pour le relais.
	params := setupParams()
	params.Port = freeLoopbackPort(t)
	keep(b.run(t, agentwire.KindResticTargetSetup, params))
	privateKey := certificateKeyOf(t, b.stateDir)
	startTargetRestServer(t, b, params.Port, seededRESTPassword)
	keep(b.runWithSecret(t, agentwire.KindResticUserAdd, agentwire.ResticUserAddParams{User: resticUser}, agentwire.ResticUserSecret{RESTPassword: seededRESTPassword}))

	// Une requête relayée : l'événement et la réponse rendue au serveur.
	server := newRelayAgentServer(t, "paquet")
	client, err := NewClient(server.URL, "", "v0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	b.runner.fetch.use(client)
	b.runner.fetch.setSession("relay-session")
	relayed := agentwire.ResticRelayRequest{ID: strings.Repeat("5", 32), Repo: resticUser, Method: http.MethodPost, Path: "/data/" + strings.Repeat("e", 64), ContentLength: 6}
	event, data, err := agentwire.ResticRelayCommand(relayed).Encode()
	if err != nil {
		t.Fatal(err)
	}
	b.runner.serveRelay(context.Background(), relayed)
	answer := receive(t, server.responses, "the relayed answer")
	if answer.head.Status != http.StatusOK {
		t.Fatalf("the bench relay did not pass: %+v", answer)
	}
	encodedHead, _ := json.Marshal(answer.head)
	outputs = append(outputs, event, string(data), string(encodedHead), answer.body)

	// La source : lien vers une cible joignable, les deux mots de passe dans
	// le secret.
	port, certificatePEM := reachableTarget(t)
	b.provision(t, "127.0.0.1", port, certificatePEM)

	// restic essaie de fuir les deux mots de passe, réussit en direct, puis
	// échoue deux fois par le relais.
	b.service(t, "forgejo")
	b.restic.answer = func(_ fakeCall, _ int, stdout, stderr io.Writer) int {
		_, _ = io.WriteString(stderr, "Fatal: rest:https://u:"+seededRESTPassword+"@nas/ password "+seededRepoPassword+"\n")
		return writeSummary(stdout)
	}
	direct := keep(b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")))
	if direct.State != agentwire.ActionSucceeded || !hasLine(direct.Output, redactedSecret) {
		t.Fatalf("direct %+v", direct)
	}
	b.runner.restic.source.dialer = &refusingDialer{}
	b.restic.answer = func(call fakeCall, _ int, _, stderr io.Writer) int {
		_, _ = io.WriteString(stderr, "unable to open "+call.env["RESTIC_REPOSITORY"]+" with "+seededRESTPassword+"/"+seededRepoPassword+"\n")
		return 1
	}
	failed := keep(b.run(t, agentwire.KindResticBackup, serviceBackup("forgejo")))
	if failed.State != agentwire.ActionFailed || failed.Restic == nil || failed.Restic.Path != agentwire.ResticPathRelay {
		t.Fatalf("relayed %+v", failed)
	}

	// La rétention sur la cible, par un vrai processus qui recopie le mot de
	// passe du dépôt sur sa sortie d'erreur.
	binary, noted := fakeResticBinary(t, true, 1)
	b.runner.checks.findRestic = func() (string, error) { return binary, nil }
	keep(b.runWithSecret(t, agentwire.KindResticRetain, agentwire.ResticRetainParams{User: resticUser, KeepDaily: 7}, agentwire.ResticRetainSecret{RepoPassword: seededRepoPassword}))
	outputs = append(outputs, readNoted(t, noted, "args"), readNoted(t, noted, "env"))

	report, _ := b.runner.resticReport(context.Background())
	encodedReport, err := json.Marshal(agentwire.SignalRequest{Restic: &report})
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(encodedReport), b.logs.String())
	for _, request := range b.relay.received() {
		outputs = append(outputs, request.method+" "+request.path)
		for name, values := range request.header {
			outputs = append(outputs, name+": "+strings.Join(values, ","))
		}
	}
	for _, call := range b.restic.callsMade() {
		outputs = append(outputs, strings.Join(call.args, " "))
		for name, value := range call.env {
			outputs = append(outputs, name+"="+value)
		}
	}

	secrets := map[string]string{"REST password": seededRESTPassword, "repository password": seededRepoPassword}
	for number, line := range strings.Split(privateKey, "\n") {
		if len(line) > 16 && !strings.HasPrefix(line, "-----") {
			secrets["private key line "+strconv.Itoa(number)] = line
		}
	}
	for label, secret := range secrets {
		for _, output := range outputs {
			if strings.Contains(output, secret) {
				t.Errorf("the %s leaked into: %.200s", label, output)
			}
		}
	}
	if !strings.Contains(string(encodedReport), "BEGIN CERTIFICATE") || len(b.restic.callsMade()) == 0 {
		t.Fatal("the bench did not see what it guards against")
	}
}
