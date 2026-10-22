package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/deploy"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const serviceID = "svc_00000000000000b1"

func nextcloud() directory.Service {
	return directory.Service{
		ID: serviceID, MachineID: "m1", Name: "nextcloud", Nature: directory.NatureApplication,
		Origin: directory.OriginCompose, ComposeProject: "nextcloud",
		Units: []directory.Unit{
			{
				Name: "app", Kind: directory.UnitContainer, Image: "ghcr.io/example/nextcloud", Version: "29.0.4",
				Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Ports:  []string{"8080:80", "443/tcp"}, DataPath: "/var/www/html",
			},
			{Name: "db", Kind: directory.UnitContainer, Image: "postgres", Version: "16.4"},
		},
	}
}

// Le fichier est écrit en entier ici : c'est ce que l'opérateur lit à
// l'écran et ce que l'agent écrit sur la machine.
const nextcloudCompose = `name: nextcloud
services:
  app:
    image: ghcr.io/example/nextcloud:29.0.4@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    restart: unless-stopped
    ports:
      - "8080:80"
      - "443/tcp"
    volumes:
      - ./data/app:/var/www/html
    env_file:
      - path: .env
        required: false
    labels:
      opencloud.service: svc_00000000000000b1
      opencloud.unit: app
  db:
    image: postgres:16.4
    restart: unless-stopped
    env_file:
      - path: .env
        required: false
    labels:
      opencloud.service: svc_00000000000000b1
      opencloud.unit: db
`

func TestRender_WritesThePinnedComposeFile(t *testing.T) {
	compose, dataDirs, err := deploy.Render(nextcloud())
	if err != nil {
		t.Fatal(err)
	}
	if string(compose) != nextcloudCompose {
		t.Fatalf("compose file:\n%s\nwant:\n%s", compose, nextcloudCompose)
	}
	if len(dataDirs) != 1 || dataDirs[0] != "app" {
		t.Fatalf("data dirs %v", dataDirs)
	}
}

// Les noms qui se liraient autrement en YAML (un nombre, un booléen) restent
// des chaînes, et l'ordre des unités n'est pas celui de l'alphabet.
func TestRender_KeepsTheUnitOrderAndQuotesWhatWouldChangeType(t *testing.T) {
	service := nextcloud()
	service.Units = []directory.Unit{
		{Name: "web", Kind: directory.UnitContainer, Image: "nginx", Version: "1.27"},
		{Name: "123", Kind: directory.UnitContainer, Image: "redis", Version: "7.4"},
		{Name: "on", Kind: directory.UnitContainer, Image: "alpine", Version: "3.20"},
	}
	compose, dataDirs, err := deploy.Render(service)
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	web, number, on := strings.Index(text, "\n  web:"), strings.Index(text, "\n  \"123\":"), strings.Index(text, "\n  \"on\":")
	if web < 0 || number < web || on < number {
		t.Fatalf("units out of order or unquoted:\n%s", text)
	}
	if len(dataDirs) != 0 || strings.Contains(text, "volumes") || strings.Contains(text, "ports") {
		t.Fatalf("a unit without ports or data path writes neither:\n%s", text)
	}
}

func TestRender_IsDeterministic(t *testing.T) {
	first, _, err := deploy.Render(nextcloud())
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		again, _, err := deploy.Render(nextcloud())
		if err != nil || string(again) != string(first) {
			t.Fatalf("a second render differs: %v\n%s", err, again)
		}
	}
}

func TestRender_RefusesWhatCannotBeDeployed(t *testing.T) {
	cases := []struct {
		name   string
		change func(*directory.Service)
		want   *refusal.Error
	}{
		{"origine container", func(s *directory.Service) { s.Origin = directory.OriginContainer }, deploy.ErrOriginNotCompose},
		{"origine binary", func(s *directory.Service) { s.Origin = directory.OriginBinary }, deploy.ErrOriginNotCompose},
		{"sans projet", func(s *directory.Service) { s.ComposeProject = "" }, deploy.ErrProjectMissing},
		{"sans unité", func(s *directory.Service) { s.Units = nil }, deploy.ErrNoUnits},
		{"unité binaire", func(s *directory.Service) { s.Units[1].Kind = directory.UnitBinary }, deploy.ErrUnitNotContainer},
		{"sans version", func(s *directory.Service) { s.Units[1].Version = "" }, deploy.ErrVersionUnpinned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := nextcloud()
			c.change(&service)
			if _, _, err := deploy.Render(service); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestRender_RefusesAFileLargerThanAnOrderCarries(t *testing.T) {
	var units []directory.Unit
	for index := range directory.MaxUnits {
		unit := directory.Unit{Name: fmt.Sprintf("unit%02d", index), Kind: directory.UnitContainer, Image: strings.Repeat("a", directory.MaxImageLength-8), Version: "3.20"}
		for range directory.MaxPorts {
			unit.Ports = append(unit.Ports, "65535:65535/udp")
		}
		units = append(units, unit)
	}
	service := nextcloud()
	service.Units = units
	if _, _, err := deploy.Render(service); !errors.Is(err, deploy.ErrComposeTooLarge) {
		t.Fatalf("got %v", err)
	}
}

// Une valeur que l'annuaire n'aurait pas dû garder (une base d'avant une
// règle, une écriture directe) ne s'écrit pas dans le fichier : le rendu la
// refuse avec la clé du champ, que la fiche traduit.
func TestRender_RefusesAHostileValueWithTheKeyOfItsField(t *testing.T) {
	const injected = "\n    privileged: true\n    volumes: [\"/:/host\"]"
	cases := []struct {
		name   string
		change func(*directory.Service)
		want   *refusal.Error
	}{
		{"projet", func(s *directory.Service) { s.ComposeProject = "nextcloud" + injected }, directory.ErrComposeProjectInvalid},
		{"nom d'unité", func(s *directory.Service) { s.Units[0].Name = "app" + injected }, directory.ErrUnitNameInvalid},
		{"nom d'unité en option", func(s *directory.Service) { s.Units[0].Name = "-app" }, directory.ErrUnitNameInvalid},
		{"image", func(s *directory.Service) { s.Units[0].Image = "nginx" + injected }, directory.ErrImageInvalid},
		{"image qui interpole l'.env", func(s *directory.Service) { s.Units[0].Image = "${DB_PASSWORD}.evil.example/app" }, directory.ErrImageInvalid},
		{"image à commentaire", func(s *directory.Service) { s.Units[0].Image = "nginx #" }, directory.ErrImageInvalid},
		{"version", func(s *directory.Service) { s.Units[0].Version = "1.0" + injected }, directory.ErrImageInvalid},
		{"version qui interpole", func(s *directory.Service) { s.Units[0].Version = "${TAG}" }, directory.ErrImageInvalid},
		{"empreinte", func(s *directory.Service) { s.Units[0].Digest = "sha256:{x}" }, directory.ErrImageInvalid},
		{"port", func(s *directory.Service) { s.Units[0].Ports = []string{"8080:80" + injected} }, directory.ErrPortsInvalid},
		{"port de l'hôte entier", func(s *directory.Service) { s.Units[0].Ports = []string{"0.0.0.0:22:22"} }, directory.ErrPortsInvalid},
		{"dossier de données", func(s *directory.Service) { s.Units[0].DataPath = "/var/www" + injected }, directory.ErrDataPathInvalid},
		{"dossier de données vers l'hôte", func(s *directory.Service) { s.Units[0].DataPath = "/x:/host" }, directory.ErrDataPathInvalid},
		{"dossier de données qui remonte", func(s *directory.Service) { s.Units[0].DataPath = "/var/../.." }, directory.ErrDataPathInvalid},
		{"trop d'unités", func(s *directory.Service) {
			for index := range directory.MaxUnits {
				s.Units = append(s.Units, directory.Unit{Name: fmt.Sprintf("u%02d", index), Kind: directory.UnitContainer, Image: "redis", Version: "7.4"})
			}
		}, directory.ErrUnitsInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service := nextcloud()
			c.change(&service)
			compose, _, err := deploy.Render(service)
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v\n%s", err, c.want, compose)
			}
		})
	}
}

type fakeServices struct{ service directory.Service }

func (f fakeServices) GetDeclaredService(_ context.Context, id string) (directory.Service, error) {
	if id != f.service.ID {
		return directory.Service{}, directory.ErrNotFound
	}
	return f.service, nil
}

type fakeMachines struct{ status machine.Status }

func (f fakeMachines) Get(context.Context, string) (machine.Status, error) { return f.status, nil }

// fakeRuns tient les ordres déjà envoyés pour le service.
type fakeRuns struct{ folder, last *action.Run }

func (f fakeRuns) LastServiceRun(context.Context, string, []agentwire.Kind) (action.Run, bool, error) {
	if f.last == nil {
		return action.Run{}, false, nil
	}
	return *f.last, true, nil
}

func (f fakeRuns) LastFolderRun(context.Context, string, []agentwire.Kind) (action.Run, bool, error) {
	if f.folder == nil {
		return action.Run{}, false, nil
	}
	return *f.folder, true, nil
}

type fakeSender struct {
	sent []action.ServiceOrder
	err  error
}

func (f *fakeSender) SendForService(_ context.Context, order action.ServiceOrder) (action.Run, error) {
	if f.err != nil {
		return action.Run{}, f.err
	}
	f.sent = append(f.sent, order)
	return action.Run{ID: "run1", Kind: order.Kind, ServiceID: order.ServiceID, MachineID: order.MachineID}, nil
}

func activeMachine(rights string) machine.Status {
	capabilities, err := machine.ParseCapabilities(rights)
	if err != nil {
		panic(err)
	}
	status := machine.Status{Online: true}
	status.ID, status.Name = "m1", "vps-paris-1"
	status.Agent = machine.Agent{
		ID: "agt_1", RootDir: "/srv/apps", EnrolledAt: time.Unix(1, 0),
		Capabilities: capabilities, ReportedAt: time.Unix(2, 0),
	}
	return status
}

type bench struct {
	service  *deploy.Service
	sender   *fakeSender
	machines *fakeMachines
	runs     *fakeRuns
}

func newBench(rights string, runs fakeRuns) *bench {
	b := &bench{sender: &fakeSender{}, machines: &fakeMachines{status: activeMachine(rights)}, runs: &runs}
	b.service = deploy.New(fakeServices{service: nextcloud()}, b.machines, b.runs, b.sender, slog.New(slog.DiscardHandler))
	return b
}

func succeededRun() *action.Run {
	return &action.Run{ID: "old", Kind: agentwire.KindDockerDeploy, State: action.StateSucceeded}
}

func TestPlan_SaysWhereTheServiceGoesAndWhatItWillBe(t *testing.T) {
	b := newBench("observe,docker.deploy,docker.update", fakeRuns{})
	plan, err := b.service.Plan(context.Background(), serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.MachineName != "vps-paris-1" || plan.RootDir != "/srv/apps" || plan.Path != "/srv/apps/nextcloud" {
		t.Fatalf("plan %+v", plan)
	}
	if plan.Deployed || plan.Right != deploy.RightOK || plan.Compose != nextcloudCompose || plan.LastRun != nil {
		t.Fatalf("plan %+v", plan)
	}

	// Une fois déployé, le droit qui compte est celui du redéploiement.
	b = newBench("observe,docker.deploy", fakeRuns{folder: succeededRun(), last: succeededRun()})
	plan, err = b.service.Plan(context.Background(), serviceID)
	if err != nil || !plan.Deployed || plan.Right != deploy.RightMissing || plan.LastRun == nil {
		t.Fatalf("plan %+v %v", plan, err)
	}

	// Avant que l'agent n'ait rapporté ses droits, c'est lui qui jugera.
	b = newBench("observe", fakeRuns{})
	b.machines.status.Agent.ReportedAt = time.Time{}
	if plan, err = b.service.Plan(context.Background(), serviceID); err != nil || plan.Right != deploy.RightUnknown {
		t.Fatalf("plan %+v %v", plan, err)
	}
}

// Un déploiement coupé (l'agent a accepté puis n'a plus donné de nouvelles)
// a pu écrire le dossier : le service ne se coince pas, il se redéploie.
func TestPlan_ATimedOutDeploymentOffersRedeploy(t *testing.T) {
	timedOut := &action.Run{ID: "cut", Kind: agentwire.KindDockerDeploy, State: action.StateFailed, Reason: action.ReasonTimeout}
	b := newBench("observe,docker.update", fakeRuns{folder: timedOut, last: timedOut})
	plan, err := b.service.Plan(context.Background(), serviceID)
	if err != nil || !plan.Deployed || plan.Right != deploy.RightOK {
		t.Fatalf("plan %+v %v", plan, err)
	}
	b = newBench("observe,docker.deploy", fakeRuns{folder: timedOut, last: timedOut})
	if plan, err = b.service.Plan(context.Background(), serviceID); err != nil || plan.Right != deploy.RightMissing {
		t.Fatalf("the right is the redeploy's: %+v %v", plan, err)
	}
}

// Un redéploiement que l'agent a refusé faute de dossier remet « Déployer » :
// un geste de plus, jamais de blocage.
func TestPlan_ARedeployThatFoundNoFolderOffersDeployAgain(t *testing.T) {
	missing := &action.Run{ID: "none", Kind: agentwire.KindDockerUpdate, State: action.StateRefused, Reason: agentwire.ReasonServiceDirMissing}
	b := newBench("observe,docker.deploy", fakeRuns{folder: missing, last: missing})
	plan, err := b.service.Plan(context.Background(), serviceID)
	if err != nil || plan.Deployed || plan.Right != deploy.RightOK {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := b.service.Deploy(context.Background(), serviceID, nil); err != nil {
		t.Fatalf("deploy again: %v", err)
	}
}

func TestPlan_RootDefaultsWhenTheAgentSaidNothing(t *testing.T) {
	b := newBench("observe,docker.deploy", fakeRuns{})
	b.machines.status.Agent.RootDir = ""
	plan, err := b.service.Plan(context.Background(), serviceID)
	if err != nil || plan.RootDir != "/srv/workspace" || plan.Path != "/srv/workspace/nextcloud" {
		t.Fatalf("plan %+v %v", plan, err)
	}
}

func TestPlan_RefusesWhatCannotBeDeployed(t *testing.T) {
	ctx := context.Background()
	b := newBench("observe,docker.deploy", fakeRuns{})
	if _, err := b.service.Plan(ctx, "svc_unknown"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("unknown service: %v", err)
	}
	for name, change := range map[string]func(*directory.Service){
		"container":    func(s *directory.Service) { s.Origin = directory.OriginContainer },
		"sans projet":  func(s *directory.Service) { s.ComposeProject = "" },
		"binaire":      func(s *directory.Service) { s.Units[0].Kind = directory.UnitBinary },
		"sans version": func(s *directory.Service) { s.Units[0].Version = "" },
	} {
		service := nextcloud()
		change(&service)
		b := deploy.New(fakeServices{service: service}, fakeMachines{status: activeMachine("observe,docker.deploy")}, fakeRuns{}, &fakeSender{}, slog.New(slog.DiscardHandler))
		_, err := b.Plan(ctx, serviceID)
		if key, ok := refusal.KeyOf(err); !ok || !strings.HasPrefix(key, "deploy.") {
			t.Errorf("%s: %v", name, err)
		}
	}
	revoked := newBench("observe,docker.deploy", fakeRuns{})
	revoked.machines.status.Agent.RevokedAt = time.Unix(3, 0)
	if _, err := revoked.service.Plan(ctx, serviceID); !errors.Is(err, deploy.ErrNoAgent) {
		t.Fatalf("revoked agent: %v", err)
	}
}

// Le .env part dans le secret de l'ordre et nulle part ailleurs : les
// paramètres, que le run garde, disent seulement qu'il est parti.
func TestDeploy_SendsTheEnvAsASecretOnly(t *testing.T) {
	b := newBench("observe,docker.deploy,docker.update", fakeRuns{})
	const env = "DB_PASSWORD=une valeur de test qui ne sort pas\nAUTRE=1\n" // gitleaks:allow
	run, err := b.service.Deploy(context.Background(), serviceID, ptr(env))
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != agentwire.KindDockerDeploy || len(b.sender.sent) != 1 {
		t.Fatalf("run %+v, sent %+v", run, b.sender.sent)
	}
	order := b.sender.sent[0]
	if order.ServiceID != serviceID || order.MachineID != "m1" || order.SecretLabel != "env" {
		t.Fatalf("order %+v", order)
	}
	var secret agentwire.DockerSecret
	if err := json.Unmarshal(order.Secret, &secret); err != nil || secret.Env != env {
		t.Fatalf("secret %s %v", order.Secret, err)
	}
	if strings.Contains(string(order.Params), "DB_PASSWORD") || strings.Contains(string(order.Params), "valeur") {
		t.Fatalf("the .env reached the params: %s", order.Params)
	}
	var params agentwire.DockerParams
	if err := json.Unmarshal(order.Params, &params); err != nil {
		t.Fatal(err)
	}
	if !params.EnvSent || params.Project != "nextcloud" || params.ServiceID != serviceID || params.Compose != nextcloudCompose ||
		len(params.DataDirs) != 1 || params.DataDirs[0] != "app" {
		t.Fatalf("params %+v", params)
	}
}

func TestDeploy_WithoutEnvSendsNoSecret(t *testing.T) {
	for name, env := range map[string]*string{"absent": nil, "vide": ptr("")} {
		b := newBench("observe,docker.deploy", fakeRuns{})
		if _, err := b.service.Deploy(context.Background(), serviceID, env); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		order := b.sender.sent[0]
		var params agentwire.DockerParams
		if err := json.Unmarshal(order.Params, &params); err != nil {
			t.Fatal(err)
		}
		if order.Secret != nil || order.SecretLabel != "" || params.EnvSent {
			t.Fatalf("%s: a secret left without a .env: %+v %+v", name, order, params)
		}
	}
}

func TestDeploy_RefusesABadEnv(t *testing.T) {
	huge := strings.Repeat("A=1\n", agentwire.MaxSecretBytes/4)
	for _, c := range []struct {
		name string
		env  string
		want *refusal.Error
	}{
		{"trop gros", huge, deploy.ErrEnvTooLarge},
		{"octet nul", "A=1\x00B=2", deploy.ErrEnvInvalid},
		{"pas de l'UTF-8", "A=\xff\xfe", deploy.ErrEnvInvalid},
	} {
		b := newBench("observe,docker.deploy", fakeRuns{})
		if _, err := b.service.Deploy(context.Background(), serviceID, ptr(c.env)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
		if len(b.sender.sent) != 0 {
			t.Errorf("%s: an order left", c.name)
		}
	}
}

func TestDeploy_RefusesASecondDeploy_AndRedeployBeforeTheFirst(t *testing.T) {
	ctx := context.Background()
	fresh := newBench("observe,docker.deploy,docker.update", fakeRuns{})
	if _, err := fresh.service.Redeploy(ctx, serviceID, nil); !errors.Is(err, deploy.ErrNotDeployed) {
		t.Fatalf("redeploy before the first deploy: %v", err)
	}
	done := newBench("observe,docker.deploy,docker.update", fakeRuns{folder: succeededRun()})
	if _, err := done.service.Deploy(ctx, serviceID, nil); !errors.Is(err, deploy.ErrAlreadyDeployed) {
		t.Fatalf("second deploy: %v", err)
	}
	if len(fresh.sender.sent)+len(done.sender.sent) != 0 {
		t.Fatal("a refused gesture sent an order")
	}
	run, err := done.service.Redeploy(ctx, serviceID, nil)
	if err != nil || run.Kind != agentwire.KindDockerUpdate || done.sender.sent[0].Kind != agentwire.KindDockerUpdate {
		t.Fatalf("redeploy: %+v %v", run, err)
	}
}

func TestDeploy_RefusesWhenTheAgentLacksTheRight(t *testing.T) {
	b := newBench("observe", fakeRuns{})
	if _, err := b.service.Deploy(context.Background(), serviceID, nil); !errors.Is(err, deploy.ErrRightMissing) {
		t.Fatalf("got %v", err)
	}
	// Redéployer exige docker.update, pas docker.deploy.
	b = newBench("observe,docker.deploy", fakeRuns{folder: succeededRun()})
	if _, err := b.service.Redeploy(context.Background(), serviceID, nil); !errors.Is(err, deploy.ErrRightMissing) {
		t.Fatalf("got %v", err)
	}
}

func TestDeploy_PassesTheSenderRefusalsOn(t *testing.T) {
	b := newBench("observe,docker.deploy", fakeRuns{})
	b.sender.err = action.ErrMachineOffline
	if _, err := b.service.Deploy(context.Background(), serviceID, nil); !errors.Is(err, action.ErrMachineOffline) {
		t.Fatalf("got %v", err)
	}
}

func ptr(text string) *string { return &text }

// L'agent rejuge les noms avec des motifs recopiés d'agentwire : ils doivent
// dire comme l'annuaire.
func TestAgentRules_AgreeWithTheDirectory(t *testing.T) {
	samples := []string{
		"nextcloud", "a", "0abc", "app_1", "my-app", "Upper", "app.one", "-lead", "_lead", ".dot", "a b", "a/b", "..",
		"é", "", strings.Repeat("a", 63), strings.Repeat("a", 64),
	}
	for _, sample := range samples {
		valid := func(project, unit string) bool {
			definition := directory.ServiceDefinition{
				MachineID: "m1", Name: "svc", Nature: directory.NatureApplication, Origin: directory.OriginCompose,
				ComposeProject: project,
				Units:          []directory.UnitDefinition{{Name: unit, Image: "nginx", Version: "1.27.0"}},
			}
			return definition.Complete().Validate() == nil
		}
		if got, want := agentwire.IsComposeProject(sample), valid(sample, "app"); got != want {
			t.Errorf("project %q: agent %v, directory %v", sample, got, want)
		}
		if got, want := agentwire.IsDataDirName(sample), valid("proj", sample); got != want {
			t.Errorf("unit %q: agent %v, directory %v", sample, got, want)
		}
	}
	if agentwire.MaxDataDirs != directory.MaxUnits {
		t.Errorf("MaxDataDirs %d, MaxUnits %d", agentwire.MaxDataDirs, directory.MaxUnits)
	}
}
