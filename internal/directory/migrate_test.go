package directory

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

var planNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// Des identifiants qui se suivent : le plan est lu sans dépendre du hasard.
func sequentialIDs() func(string) (string, error) {
	next := 0
	return func(prefix string) (string, error) {
		next++
		return fmt.Sprintf("%s%016x", prefix, next), nil
	}
}

func linkedSet(fiches ...container.Container) map[string]bool {
	linked := map[string]bool{}
	for _, fiche := range fiches {
		linked[fiche.ID] = true
	}
	return linked
}

func archived(fiche container.Container) container.Container {
	fiche.ArchivedAt = planNow.Add(-time.Hour)
	fiche.State = container.StateExited
	return fiche
}

func withImage(fiche container.Container, image string) container.Container {
	fiche.Image = image
	return fiche
}

func seenAt(fiche container.Container, at time.Time) container.Container {
	fiche.LastSeenAt = at
	return fiche
}

func plan(t *testing.T, fiches []container.Container, linked map[string]bool, declared []Service) LinkPlan {
	t.Helper()
	got, err := PlanLinks(fiches, linked, declared, sequentialIDs())
	if err != nil {
		t.Fatalf("PlanLinks: %v", err)
	}
	return got
}

func linkOf(t *testing.T, got LinkPlan, item container.Container) LinkedFiche {
	t.Helper()
	for _, link := range got.Links {
		if link.ContainerID == item.ID {
			return link
		}
	}
	t.Fatalf("la fiche %s n'est pas dans le plan: %+v", item.Name, got.Links)
	return LinkedFiche{}
}

func unitNames(created Service) []string {
	var names []string
	for _, unit := range created.Units {
		names = append(names, unit.Name)
	}
	return names
}

// Un projet Compose dont un conteneur porte un lien devient un service, avec
// une unité par service Compose du projet : les conteneurs sans lien y sont
// aussi, sinon l'inventaire les verrait comme un écart à la première minute.
func TestPlanLinks_ComposeProjectBecomesOneServiceWithAllItsUnits(t *testing.T) {
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	db := composeContainer("m1", "nextcloud-db-1", "nextcloud", "db")
	redis := composeContainer("m1", "nextcloud-redis-1", "nextcloud", "redis")

	got := plan(t, []container.Container{redis, db, app}, linkedSet(app), nil)

	if len(got.Created) != 1 {
		t.Fatalf("un seul service attendu: %+v", got.Created)
	}
	created := got.Created[0]
	if created.Name != "nextcloud" || created.Origin != OriginCompose || created.ComposeProject != "nextcloud" {
		t.Fatalf("service = %+v", created)
	}
	if created.DeclaredBy != ByMigration || !created.NeedsReview || created.Nature != NatureApplication {
		t.Fatalf("un service repris se relit: %+v", created)
	}
	if want := []string{"app", "db", "redis"}; !slices.Equal(unitNames(created), want) {
		t.Fatalf("unités = %v, want %v", unitNames(created), want)
	}
	// Toutes les fiches de la clé reçoivent leur unité, liées ou non : le
	// rapprochement de l'inventaire n'aura rien à déplacer au premier rapport.
	if len(got.Links) != 3 {
		t.Fatalf("liens = %+v", got.Links)
	}
	for index, fiche := range []container.Container{app, db, redis} {
		link := linkOf(t, got, fiche)
		if link.ServiceID != created.ID || link.Level != container.MatchCompose || link.UnitID != created.Units[index].ID {
			t.Fatalf("lien de %s = %+v, unités %+v", fiche.Name, link, created.Units)
		}
	}
}

func TestPlanLinks_LoneContainerBecomesAContainerService(t *testing.T) {
	whoami := aloneContainer("m1", "whoami")
	other := aloneContainer("m1", "other")

	got := plan(t, []container.Container{whoami, other}, linkedSet(whoami), nil)

	if len(got.Created) != 1 {
		t.Fatalf("un conteneur sans lien n'est pas repris: %+v", got.Created)
	}
	created := got.Created[0]
	unit := created.Units[0]
	if created.Origin != OriginContainer || created.Name != "whoami" || unit.ContainerName != "whoami" || unit.Image != "traefik/whoami" || unit.Version != "v1" {
		t.Fatalf("service = %+v", created)
	}
	if link := linkOf(t, got, whoami); link.Level != container.MatchName || link.UnitID != unit.ID {
		t.Fatalf("lien = %+v", link)
	}
}

// La fiche archivée qui porte la sonde et la fiche vivante qui l'a remplacée
// donnent une seule unité : c'est ce qui rend son lien à un conteneur déjà
// recréé. Une fiche liée archivée seule garde, elle aussi, son unité.
func TestPlanLinks_ArchivedOnlyFicheStillCreatesTheUnit(t *testing.T) {
	old := archived(composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"))
	old.ID = container.ID("m1", "old-docker-id")
	fresh := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")

	got := plan(t, []container.Container{fresh, old}, linkedSet(old), nil)

	if len(got.Created) != 1 || len(got.Created[0].Units) != 1 {
		t.Fatalf("un service, une unité: %+v", got.Created)
	}
	unit := got.Created[0].Units[0]
	if linkOf(t, got, old).UnitID != unit.ID {
		t.Fatalf("la fiche archivée va à l'unité: %+v", got.Links)
	}

	// Seule, archivée : l'unité existe quand même, la sonde garde son service.
	alone := archived(aloneContainer("m1", "gone"))
	got = plan(t, []container.Container{alone}, linkedSet(alone), nil)
	if len(got.Created) != 1 || linkOf(t, got, alone).ServiceID != got.Created[0].ID {
		t.Fatalf("plan = %+v", got)
	}
}

// Le service que l'opérateur a déjà déclaré est réutilisé, jamais doublé. Un
// projet déclaré sans le service Compose d'un conteneur lié le gagne, et se
// relit.
func TestPlanLinks_DeclaredServiceReusedAndMarkedForReview(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	redis := composeContainer("m1", "nextcloud-redis-1", "nextcloud", "redis")

	got := plan(t, []container.Container{app, redis}, linkedSet(app, redis), declared)

	if len(got.Created) != 0 {
		t.Fatalf("rien à créer: %+v", got.Created)
	}
	if len(got.Added) != 1 || got.Added[0].Name != "redis" || got.Added[0].ServiceID != "svc_1" {
		t.Fatalf("ajouts = %+v", got.Added)
	}
	if !slices.Equal(got.Reviewed, []string{"svc_1"}) {
		t.Fatalf("à relire = %v", got.Reviewed)
	}
	if link := linkOf(t, got, app); link.UnitID != "unit_nextcloud_app" || link.ServiceID != "svc_1" {
		t.Fatalf("app = %+v", link)
	}
	if link := linkOf(t, got, redis); link.UnitID != got.Added[0].ID || link.ServiceID != "svc_1" || link.Level != container.MatchCompose {
		t.Fatalf("redis = %+v", link)
	}
}

// Un service que l'opérateur a déclaré sans qu'aucun lien ne le demande ne
// bouge pas : ni unité ajoutée, ni marque.
func TestPlanLinks_DeclaredServiceWithoutLinkIsLeftAlone(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	redis := composeContainer("m1", "nextcloud-redis-1", "nextcloud", "redis")

	got := plan(t, []container.Container{app, redis}, linkedSet(app), declared)

	if len(got.Added) != 0 || len(got.Reviewed) != 0 || len(got.Created) != 0 {
		t.Fatalf("rien ne doit changer: %+v", got)
	}
	if len(got.Links) != 1 {
		t.Fatalf("liens = %+v", got.Links)
	}
}

// Une fiche déjà rattachée garde son unité, même si la règle du rapprochement
// en choisirait une autre.
func TestPlanLinks_AttachedFicheKeepsItsUnit(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app", "db")}
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	app.UnitID, app.MatchLevel = "unit_nextcloud_db", container.MatchName

	got := plan(t, []container.Container{app}, linkedSet(app), declared)

	if link := linkOf(t, got, app); link.UnitID != "unit_nextcloud_db" || link.Level != container.MatchName {
		t.Fatalf("lien = %+v", link)
	}

	// Une unité que l'annuaire ne tient plus est une erreur, pas un lien perdu.
	app.UnitID = "unit_disparue"
	if _, err := PlanLinks([]container.Container{app}, linkedSet(app), declared, sequentialIDs()); !errors.Is(err, ErrLinkUnplanned) {
		t.Fatalf("attendu %v, obtenu %v", ErrLinkUnplanned, err)
	}
}

// Quoi qu'on lui donne, le plan place toute fiche liée ou échoue : jamais de
// lien perdu en silence.
func TestPlanLinks_EveryLinkedFicheIsMapped(t *testing.T) {
	app := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	whoami := aloneContainer("m1", "whoami")
	odd := composeContainer("m1", "Odd_1", "odd", "bad service name")
	badProject := composeContainer("m1", "My_App-web-1", "My_App", "web")
	broken := aloneContainer("m1", "broken-image")
	broken.Image = "NOT A VALID IMAGE"
	gone := archived(aloneContainer("m2", "gone"))
	fiches := []container.Container{app, whoami, odd, badProject, broken, gone}
	linked := linkedSet(fiches...)

	got := plan(t, fiches, linked, nil)

	for _, fiche := range fiches {
		linkOf(t, got, fiche)
	}
	// Un lien qui vise une fiche que la base n'a pas : arrêt, pas oubli.
	linked["m1:inconnue"] = true
	if _, err := PlanLinks(fiches, linked, nil, sequentialIDs()); !errors.Is(err, ErrLinkUnplanned) {
		t.Fatalf("attendu %v, obtenu %v", ErrLinkUnplanned, err)
	}
}

// Ce que le plan crée doit passer la validation de l'annuaire : un service
// repris que l'opérateur ne pourrait pas enregistrer après coup est une
// migration qui a mal tourné.
func TestPlanLinks_CreatedDefinitionsPassValidate(t *testing.T) {
	fiches := []container.Container{
		// Un projet ou un service Compose au nom que l'annuaire refuse : le
		// conteneur descend en conteneur seul.
		withImage(composeContainer("m1", "My_App-web-1", "My_App", "web"), "nginx:1.27"),
		withImage(composeContainer("m1", "x-1", "shop", "web server"), "nginx:1.27"),
		aloneContainer("m1", "Weird.Name_9"),
		func() container.Container {
			c := aloneContainer("m1", "latest-user")
			c.Image = "registry.example.org:5000/team/app:latest"
			return c
		}(),
	}

	got := plan(t, fiches, linkedSet(fiches...), nil)

	if len(got.Created) == 0 {
		t.Fatal("rien n'a été créé")
	}
	for _, created := range got.Created {
		if err := created.Definition().Complete().Validate(); err != nil {
			t.Errorf("le service %q ne passe pas Validate: %v (%+v)", created.Name, err, created)
		}
	}
}

// Un nom déjà pris par l'opérateur, ou par un service que le plan vient de
// créer, reçoit un suffixe ; le nom ne se partage qu'au sein d'une machine.
func TestPlanLinks_NameCollisionGetsASuffix(t *testing.T) {
	declared := []Service{declaredContainer("svc_1", "m1", "whoami")}
	declared[0].Name = "web"
	project := composeContainer("m1", "web-app-1", "web", "app")
	lone := aloneContainer("m1", "Web")
	other := composeContainer("m2", "web-app-1", "web", "app")

	got := plan(t, []container.Container{project, lone, other}, linkedSet(project, lone, other), declared)

	names := map[string][]string{}
	for _, created := range got.Created {
		names[created.MachineID] = append(names[created.MachineID], created.Name)
	}
	if !slices.Equal(names["m1"], []string{"web-2", "web-3"}) {
		t.Fatalf("m1 = %v", names["m1"])
	}
	if !slices.Equal(names["m2"], []string{"web"}) {
		t.Fatalf("m2 = %v", names["m2"])
	}
}

// Plus de 32 services Compose dans un projet : le service en garde 32, ceux
// qu'un lien vise d'abord, et les fiches liées qui ne tiennent pas descendent
// en conteneurs seuls plutôt que de faire échouer le démarrage.
func TestPlanLinks_ProjectOverTheUnitLimitKeepsLinkedOnesAndNeverFails(t *testing.T) {
	var fiches []container.Container
	for index := range MaxUnits + 3 {
		fiches = append(fiches, composeContainer("m1", fmt.Sprintf("big-s%02d-1", index), "big", fmt.Sprintf("s%02d", index)))
	}
	linked := linkedSet(fiches[0], fiches[MaxUnits+2])

	got := plan(t, fiches, linked, nil)

	var project Service
	for _, created := range got.Created {
		if created.Origin == OriginCompose {
			project = created
		}
	}
	if len(project.Units) != MaxUnits {
		t.Fatalf("unités = %d, want %d", len(project.Units), MaxUnits)
	}
	if !slices.Contains(unitNames(project), "s34") {
		t.Fatalf("la dernière fiche liée doit tenir dans le service: %v", unitNames(project))
	}
	if err := project.Definition().Complete().Validate(); err != nil {
		t.Fatalf("le service tronqué doit rester valable: %v", err)
	}
	for _, id := range []string{fiches[0].ID, fiches[MaxUnits+2].ID} {
		if !slices.ContainsFunc(got.Links, func(link LinkedFiche) bool { return link.ContainerID == id }) {
			t.Fatalf("la fiche %s n'est pas placée", id)
		}
	}
}

// Le plan ne dépend ni de l'ordre des fiches ni de celui des maps : deux
// démarrages sur la même base donnent les mêmes services, dans le même ordre.
func TestPlanLinks_IsDeterministic(t *testing.T) {
	fiches := []container.Container{
		composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"),
		composeContainer("m1", "nextcloud-db-1", "nextcloud", "db"),
		composeContainer("m2", "blog-web-1", "blog", "web"),
		aloneContainer("m1", "whoami"),
		archived(aloneContainer("m2", "gone")),
	}
	linked := linkedSet(fiches...)
	want := plan(t, fiches, linked, nil)

	for round := range 20 {
		shuffled := slices.Clone(fiches)
		slices.Reverse(shuffled)
		if round%2 == 1 {
			shuffled[0], shuffled[2] = shuffled[2], shuffled[0]
		}
		got := plan(t, shuffled, linked, nil)
		// Les identifiants se suivent dans l'ordre du plan : tout égal.
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tour %d : plan différent\n got  %+v\n want %+v", round, got, want)
		}
	}
}

// La dernière fiche vue dit l'image de l'unité : une fiche vivante avant une
// archivée, puis la plus récente.
func TestPlanLinks_UnitReadsTheLatestFiche(t *testing.T) {
	older := seenAt(composeContainer("m1", "shop-web-1", "shop", "web"), planNow.Add(-48*time.Hour))
	older.ID = container.ID("m1", "older")
	older.Image = "nginx:1.25"
	older = archived(older)
	newer := seenAt(composeContainer("m1", "shop-web-1", "shop", "web"), planNow)
	newer.Image = "nginx:1.27"

	got := plan(t, []container.Container{older, newer}, linkedSet(older), nil)

	unit := got.Created[0].Units[0]
	if unit.Image != "nginx" || unit.Version != "1.27" {
		t.Fatalf("unité = %+v", unit)
	}
}

// Le conteneur `init` d'un projet, terminé en 0, n'est pas une unité : une unité
// sans conteneur qui tourne ferait passer le composant de statut en panne sans
// que rien n'ait changé. Il reste non déclaré, visible dans l'écart.
func TestPlanLinks_StoppedContainerNothingPointsAtIsNotAUnit(t *testing.T) {
	app := composeContainer("m1", "cloud-app-1", "cloud", "app")
	initial := composeContainer("m1", "cloud-init-1", "cloud", "init")
	initial.State = container.StateExited
	restarting := composeContainer("m1", "cloud-worker-1", "cloud", "worker")
	restarting.State = container.StateRestarting
	paused := composeContainer("m1", "cloud-cron-1", "cloud", "cron")
	paused.State = container.StatePaused

	got := plan(t, []container.Container{app, initial, restarting, paused}, linkedSet(app), nil)

	if len(got.Created) != 1 {
		t.Fatalf("un seul service attendu: %+v", got.Created)
	}
	if names := unitNames(got.Created[0]); !slices.Equal(names, []string{"app", "worker"}) {
		t.Fatalf("unités = %v, want app et worker (qui redémarre)", names)
	}
	for _, left := range []container.Container{initial, paused} {
		if slices.ContainsFunc(got.Links, func(link LinkedFiche) bool { return link.ContainerID == left.ID }) {
			t.Errorf("%s n'est visé par rien et ne tourne pas : il reste non déclaré", left.Name)
		}
	}
}

// Un conteneur arrêté que l'opérateur a lié (une sonde le vise) garde son
// unité : le lien l'emporte sur l'état.
func TestPlanLinks_StoppedContainerThatIsLinkedKeepsItsUnit(t *testing.T) {
	app := composeContainer("m1", "cloud-app-1", "cloud", "app")
	app.State = container.StateExited

	got := plan(t, []container.Container{app}, linkedSet(app), nil)

	if len(got.Created) != 1 || !slices.Equal(unitNames(got.Created[0]), []string{"app"}) {
		t.Fatalf("service = %+v", got.Created)
	}
}

// Un projet que l'opérateur a déjà déclaré avec 32 unités ne reçoit plus rien :
// la fiche liée d'un 33e service Compose descend en conteneur seul. Créer un
// second service Compose pour (machine, projet) violerait l'unicité et la
// migration ne démarrerait plus.
func TestPlanLinks_FullDeclaredProjectNeverGetsASecondComposeService(t *testing.T) {
	var names []string
	for index := range MaxUnits {
		names = append(names, fmt.Sprintf("u%02d", index))
	}
	declared := []Service{declaredCompose("svc_big", "m1", "big", names...)}
	extra := composeContainer("m1", "big-extra-1", "big", "extra")
	other := composeContainer("m1", "big-other-1", "big", "other")

	got := plan(t, []container.Container{extra, other}, linkedSet(extra), declared)

	if len(got.Added) != 0 || len(got.Reviewed) != 0 {
		t.Fatalf("le service plein ne reçoit rien: %+v", got)
	}
	if len(got.Created) != 1 {
		t.Fatalf("un conteneur seul attendu: %+v", got.Created)
	}
	created := got.Created[0]
	if created.Origin != OriginContainer || created.ComposeProject != "" || created.Units[0].ContainerName != "big-extra-1" {
		t.Fatalf("service = %+v", created)
	}
	if link := linkOf(t, got, extra); link.ServiceID != created.ID || link.Level != container.MatchName {
		t.Fatalf("lien = %+v", link)
	}
	if slices.ContainsFunc(got.Links, func(link LinkedFiche) bool { return link.ContainerID == other.ID }) {
		t.Fatal("la fiche sans lien du projet reste non déclarée")
	}
	if err := created.Definition().Complete().Validate(); err != nil {
		t.Fatalf("le service ne passe pas Validate: %v", err)
	}
}

// Un conteneur seul dont le nom est déjà celui d'une unité déclarée (d'un
// service Compose, que le rapprochement par nom ignore) va à cette unité : en
// créer une autre ferait deux services pour une même clé d'observation.
func TestPlanLinks_ContainerNameAlreadyHeldByADeclaredUnitJoinsIt(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "shop", "web")}
	declared[0].Units[0].ContainerName = "shop-front"
	front := aloneContainer("m1", "shop-front")
	otherMachine := aloneContainer("m2", "shop-front")

	got := plan(t, []container.Container{front, otherMachine}, linkedSet(front, otherMachine), declared)

	if link := linkOf(t, got, front); link.UnitID != "unit_shop_web" || link.ServiceID != "svc_1" {
		t.Fatalf("le conteneur doit rejoindre l'unité qui porte son nom: %+v", link)
	}
	// Le nom n'est pris que sur sa machine.
	if len(got.Created) != 1 || got.Created[0].MachineID != "m2" {
		t.Fatalf("créés = %+v", got.Created)
	}
}

// Une fiche que rien ne vise mais que le rapprochement rattache à un service
// déclaré reçoit son unité aussi, archivée comprise : l'inventaire ne
// recalcule jamais une fiche archivée.
func TestPlanLinks_UnlinkedFichesOfADeclaredServiceAreAttachedToo(t *testing.T) {
	declared := []Service{declaredCompose("svc_1", "m1", "nextcloud", "app")}
	linkedFiche := composeContainer("m1", "nextcloud-app-1", "nextcloud", "app")
	old := archived(composeContainer("m1", "nextcloud-app-1", "nextcloud", "app"))
	old.ID = container.ID("m1", "old-docker-id")

	got := plan(t, []container.Container{linkedFiche, old}, linkedSet(linkedFiche), declared)

	for _, fiche := range []container.Container{linkedFiche, old} {
		if link := linkOf(t, got, fiche); link.UnitID != "unit_nextcloud_app" || link.ServiceID != "svc_1" {
			t.Errorf("%s = %+v", fiche.ID, link)
		}
	}
}
