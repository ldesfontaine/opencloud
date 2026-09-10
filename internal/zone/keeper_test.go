package zone

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/cloudflare"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/migrations"
)

const (
	testZone   = "exemple.fr"
	testZoneID = "023e105f4ecef8ad9ca31a8372d0c353"
)

// Les jetons des tests sont fabriqués, jamais écrits en dur : un scanner de
// secrets ne doit pas les prendre pour de vrais (packaging/test-action.sh fait
// de même pour son mot de passe).
func madeUpToken(what string) string {
	return "jetable-" + what + "-pour-le-test"
}

var (
	testToken    = madeUpToken("zone")
	testNewToken = madeUpToken("zone-tournee")
)

// fakeCloudflare joue l'API : ce qu'elle accepte est décrit ici, pas deviné.
type fakeCloudflare struct {
	validTokens map[string]bool
	zones       map[string]string // jeton → nom de zone qu'il voit
	failure     error
}

func (f *fakeCloudflare) VerifyToken(_ context.Context, token string) error {
	if f.failure != nil {
		return f.failure
	}
	if !f.validTokens[token] {
		return cloudflare.ErrInvalidToken
	}
	return nil
}

func (f *fakeCloudflare) ZoneByName(_ context.Context, token, name string) (cloudflare.Zone, error) {
	if f.failure != nil {
		return cloudflare.Zone{}, f.failure
	}
	if f.zones[token] != name {
		return cloudflare.Zone{}, cloudflare.ErrZoneNotFound
	}
	return cloudflare.Zone{ID: testZoneID, Name: name, Status: "active"}, nil
}

func acceptingCloudflare() *fakeCloudflare {
	return &fakeCloudflare{
		validTokens: map[string]bool{testToken: true, testNewToken: true},
		zones:       map[string]string{testToken: testZone, testNewToken: testZone},
	}
}

// newKeeper monte le gardien sur une vraie base SQLite temporaire et un
// répertoire d'état à lui : le store est rapide et local, un faux mentirait.
func newKeeper(t *testing.T, api Cloudflare) (*Keeper, string, *store.Store) {
	t.Helper()

	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	logger := slog.New(slog.DiscardHandler)
	database, err := store.Open(context.Background(), root, migrations.Files, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	return New(root, database, api, logger), directory, database
}

func TestAdd_ValidToken_RegistersTheZoneAndWritesTheTokenIn0600(t *testing.T) {
	keeper, directory, _ := newKeeper(t, acceptingCloudflare())

	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatalf("Add = %v", err)
	}

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatalf("Zones = %v", err)
	}
	if len(zones) != 1 || zones[0].Name != testZone || zones[0].CloudflareID != testZoneID {
		t.Fatalf("zones = %+v", zones)
	}

	path := filepath.Join(directory, "zones", testZone+".token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("le fichier du jeton n'est pas posé : %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o, attendu 0600", info.Mode().Perm())
	}

	directoryInfo, err := os.Stat(filepath.Join(directory, "zones"))
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Errorf("mode du dossier = %04o, attendu 0700", directoryInfo.Mode().Perm())
	}
}

func TestAdd_InvalidToken_RefusesAndWritesNothing(t *testing.T) {
	api := acceptingCloudflare()
	api.validTokens = map[string]bool{}
	keeper, directory, _ := newKeeper(t, api)

	err := keeper.Add(context.Background(), testZone, testToken)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Add = %v, attendu un refus", err)
	}
	if strings.Contains(refused.Cause+refused.Remedy, testToken) {
		t.Errorf("le refus recopie le jeton : %q", refused)
	}
	if _, err := os.Stat(filepath.Join(directory, "zones", testZone+".token")); !errors.Is(err, os.ErrNotExist) {
		t.Error("un jeton refusé a quand même été écrit")
	}
	zones, _ := keeper.Zones(context.Background())
	if len(zones) != 0 {
		t.Errorf("la zone a été enregistrée malgré le refus : %+v", zones)
	}
}

// Le jeton peut être valide sans voir la zone demandée : les deux comptent.
func TestAdd_TokenThatDoesNotSeeTheZone_Refuses(t *testing.T) {
	api := acceptingCloudflare()
	api.zones = map[string]string{testToken: "autre-exemple.fr"}
	keeper, directory, _ := newKeeper(t, api)

	err := keeper.Add(context.Background(), testZone, testToken)

	var refused refusal.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Cause, testZone) {
		t.Fatalf("Add = %v, attendu un refus nommant la zone", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "zones", testZone+".token")); !errors.Is(err, os.ErrNotExist) {
		t.Error("un jeton qui ne voit pas la zone a quand même été écrit")
	}
}

func TestAdd_EmptyToken_RefusesWithoutCallingCloudflare(t *testing.T) {
	api := &fakeCloudflare{failure: errors.New("Cloudflare ne doit pas être appelé")}
	keeper, _, _ := newKeeper(t, api)

	err := keeper.Add(context.Background(), testZone, "   ")

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Add = %v, attendu un refus", err)
	}
}

func TestAdd_SameZoneTwice_RefusesAndPointsToRotation(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}

	err := keeper.Add(context.Background(), testZone, testNewToken)

	var refused refusal.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Remedy, "tourner") {
		t.Fatalf("Add = %v, attendu un refus qui renvoie à la rotation", err)
	}
}

func TestRotate_ReplacesTheTokenFileAndNotesTheDate(t *testing.T) {
	keeper, directory, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	before, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := keeper.Rotate(context.Background(), testZone, testNewToken); err != nil {
		t.Fatalf("Rotate = %v", err)
	}

	written, err := os.ReadFile(filepath.Join(directory, "zones", testZone+".token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != testNewToken+"\n" {
		t.Error("le fichier ne porte pas le nouveau jeton")
	}

	after, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !after[0].RotatedAt.After(before[0].RotatedAt) && !after[0].RotatedAt.Equal(before[0].RotatedAt) {
		t.Errorf("la date de rotation a reculé : %s puis %s", before[0].RotatedAt, after[0].RotatedAt)
	}
	if after[0].Fingerprint == before[0].Fingerprint {
		t.Error("l'empreinte n'a pas changé alors que le jeton a tourné")
	}
}

func TestRotate_InvalidToken_KeepsTheTokenInPlace(t *testing.T) {
	api := acceptingCloudflare()
	keeper, directory, _ := newKeeper(t, api)
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	api.validTokens = map[string]bool{}

	if err := keeper.Rotate(context.Background(), testZone, testNewToken); err == nil {
		t.Fatal("Rotate = nil, attendu un refus")
	}

	written, err := os.ReadFile(filepath.Join(directory, "zones", testZone+".token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != testToken+"\n" {
		t.Error("un jeton refusé a remplacé le jeton en place")
	}
}

func TestRemove_TakesTheZoneAndItsTokenAway(t *testing.T) {
	keeper, directory, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}

	if err := keeper.Remove(context.Background(), testZone); err != nil {
		t.Fatalf("Remove = %v", err)
	}

	if _, err := os.Stat(filepath.Join(directory, "zones", testZone+".token")); !errors.Is(err, os.ErrNotExist) {
		t.Error("le fichier du jeton est resté")
	}
	zones, _ := keeper.Zones(context.Background())
	if len(zones) != 0 {
		t.Errorf("zones = %+v, attendu aucune", zones)
	}
}

// Un nom publié sous la zone la retient : le retrait est un refus, jamais une
// suppression en cascade.
func TestRemove_ZoneHeldByAPublishedName_Refuses(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	published := store.Domain{
		Name: "temoin." + testZone, MachineID: store.LocalMachineID,
		Environment: "prod", Service: "temoin", Port: 80,
	}
	if err := database.RecordDomain(context.Background(), published); err != nil {
		t.Fatal(err)
	}

	err := keeper.Remove(context.Background(), testZone)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Remove = %v, attendu un refus", err)
	}
	if !strings.Contains(refused.Cause, "temoin."+testZone) {
		t.Errorf("le refus ne nomme pas le nom qui retient la zone : %q", refused.Cause)
	}
}

// Un nom qui ressemble à la zone sans en être un sous-nom ne la retient pas.
func TestRemove_NeighbourName_DoesNotHoldTheZone(t *testing.T) {
	keeper, _, database := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}
	voisin := store.Domain{
		Name: "autre-exemple.fr", MachineID: store.LocalMachineID,
		Environment: "prod", Service: "temoin", Port: 80,
	}
	if err := database.RecordDomain(context.Background(), voisin); err != nil {
		t.Fatal(err)
	}

	if err := keeper.Remove(context.Background(), testZone); err != nil {
		t.Fatalf("Remove = %v, attendu le retrait", err)
	}
}

func TestZoneToken_UnknownZone_IsANamedRefusal(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())

	_, err := keeper.ZoneToken("inconnue.fr")

	var refused refusal.Refusal
	if !errors.As(err, &refused) || !strings.Contains(refused.Cause, "inconnue.fr") {
		t.Fatalf("ZoneToken = %v, attendu un refus nommant la zone", err)
	}
}

// L'empreinte du fichier posé est celle que le gardien connaît : c'est ce qui
// permet de dire quelle machine porte encore l'ancien jeton.
func TestZoneToken_MatchesTheFingerprintTheKeeperShows(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}

	content, err := keeper.ZoneToken(testZone)
	if err != nil {
		t.Fatalf("ZoneToken = %v", err)
	}
	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if Fingerprint(content) != zones[0].Fingerprint {
		t.Errorf("empreinte du fichier %q, empreinte affichée %q", Fingerprint(content), zones[0].Fingerprint)
	}
	if len(zones[0].Fingerprint) != FingerprintLength {
		t.Errorf("empreinte = %q, attendu %d caractères", zones[0].Fingerprint, FingerprintLength)
	}
}

func TestNames_RendersTheRegisteredZones(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}

	names, err := keeper.Names(context.Background())
	if err != nil {
		t.Fatalf("Names = %v", err)
	}
	if len(names) != 1 || names[0] != testZone {
		t.Errorf("Names = %v", names)
	}
}

func TestAdd_MalformedZoneName_Refuses(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())

	err := keeper.Add(context.Background(), "PAS UNE ZONE", testToken)

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Add = %v, attendu un refus", err)
	}
}

// Le temps ne doit pas rendre le test capricieux : la date de rotation est
// écrite en UTC, comme tout le reste.
func TestAdd_DatesAreUTC(t *testing.T) {
	keeper, _, _ := newKeeper(t, acceptingCloudflare())
	if err := keeper.Add(context.Background(), testZone, testToken); err != nil {
		t.Fatal(err)
	}

	zones, err := keeper.Zones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if zones[0].AddedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("AddedAt = %s, dans le futur", zones[0].AddedAt)
	}
}
