package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// fakeGitHub sert ce que self-update demande à l'API : releases, fichiers,
// attestations. Tout est en mémoire, rien ne sort du test.
type fakeGitHub struct {
	server       *httptest.Server
	mutex        sync.Mutex
	releases     map[string]fakeRelease
	attestations map[string][]string
	requests     []*http.Request
	// beforeAsset, s'il est posé avant le premier appel, s'exécute au début de
	// chaque téléchargement de fichier.
	beforeAsset func(name string)
}

type fakeRelease struct {
	tag        string
	assets     map[string][]byte
	prerelease bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fake := &fakeGitHub{releases: map[string]fakeRelease{}, attestations: map[string][]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/ldesfontaine/opencloud/releases/latest", fake.serveLatest)
	mux.HandleFunc("GET /repos/ldesfontaine/opencloud/releases/tags/{tag}", fake.serveByTag)
	mux.HandleFunc("GET /repos/ldesfontaine/opencloud/releases", fake.serveList)
	mux.HandleFunc("GET /repos/ldesfontaine/opencloud/releases/assets/{tag}/{name}", fake.serveAsset)
	mux.HandleFunc("GET /repos/ldesfontaine/opencloud/attestations/{subject}", fake.serveAttestations)
	fake.server = httptest.NewServer(fake.record(mux))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeGitHub) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mutex.Lock()
		f.requests = append(f.requests, r)
		f.mutex.Unlock()
		next.ServeHTTP(w, r)
	})
}

// addRelease publie une version avec son binaire et un SHA256SUMS juste.
func (f *fakeGitHub) addRelease(tag string, binary []byte) {
	name := "opencloud_" + strings.TrimPrefix(tag, "v") + "_linux_amd64"
	sum := sha256.Sum256(binary)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	f.releases[tag] = fakeRelease{tag: tag, assets: map[string][]byte{name: binary, checksumsAssetName: []byte(sums)}}
}

// addPrerelease publie une préversion : elle existe sur GitHub, elle ne
// s'installe pas.
func (f *fakeGitHub) addPrerelease(tag string, binary []byte) {
	f.addRelease(tag, binary)
	release := f.releases[tag]
	release.prerelease = true
	f.releases[tag] = release
}

func (f *fakeGitHub) attest(binary []byte, bundle string) {
	sum := sha256.Sum256(binary)
	key := "sha256:" + hex.EncodeToString(sum[:])
	f.attestations[key] = append(f.attestations[key], bundle)
}

func (f *fakeGitHub) releaseJSON(release fakeRelease) map[string]any {
	var assets []map[string]any
	for name, content := range release.assets {
		assets = append(assets, map[string]any{
			"name": name,
			"url":  f.server.URL + "/repos/ldesfontaine/opencloud/releases/assets/" + release.tag + "/" + name,
			"size": len(content),
		})
	}
	return map[string]any{"tag_name": release.tag, "draft": false, "prerelease": release.prerelease, "assets": assets}
}

func (f *fakeGitHub) serveLatest(w http.ResponseWriter, r *http.Request) {
	var latest fakeRelease
	found := false
	for _, release := range f.releases {
		if release.prerelease {
			continue
		}
		version, _ := ParseVersion(release.tag)
		latestVersion, _ := ParseVersion(latest.tag)
		if !found || version.Compare(latestVersion) > 0 {
			latest = release
			found = true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, f.releaseJSON(latest))
}

func (f *fakeGitHub) serveByTag(w http.ResponseWriter, r *http.Request) {
	release, found := f.releases[r.PathValue("tag")]
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, f.releaseJSON(release))
}

func (f *fakeGitHub) serveList(w http.ResponseWriter, _ *http.Request) {
	var list []map[string]any
	for _, release := range f.releases {
		list = append(list, f.releaseJSON(release))
	}
	writeJSON(w, list)
}

func (f *fakeGitHub) serveAsset(w http.ResponseWriter, r *http.Request) {
	if f.beforeAsset != nil {
		f.beforeAsset(r.PathValue("name"))
	}
	release, found := f.releases[r.PathValue("tag")]
	if !found {
		http.NotFound(w, r)
		return
	}
	content, found := release.assets[r.PathValue("name")]
	if !found {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(content)
}

func (f *fakeGitHub) serveAttestations(w http.ResponseWriter, r *http.Request) {
	bundles, found := f.attestations[r.PathValue("subject")]
	if !found {
		http.NotFound(w, r)
		return
	}
	var attestations []map[string]any
	for _, bundle := range bundles {
		attestations = append(attestations, map[string]any{"bundle": json.RawMessage(bundle)})
	}
	writeJSON(w, map[string]any{"attestations": attestations})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// fakeVerifier note ce qu'on lui demande et répond ce qu'on lui a dit.
type fakeVerifier struct {
	err      error
	bundles  [][]byte
	identity verify.CertificateIdentity
}

func (v *fakeVerifier) Verify(_ context.Context, bundles [][]byte, _ [sha256.Size]byte, identity verify.CertificateIdentity) error {
	v.bundles = bundles
	v.identity = identity
	return v.err
}

type fakeRestart struct {
	calls int
	err   error
}

func (r *fakeRestart) restart(context.Context) error {
	r.calls++
	return r.err
}

type testUpdater struct {
	updater    *Updater
	out        *bytes.Buffer
	verifier   *fakeVerifier
	restart    *fakeRestart
	executable string
}

// newTestUpdater installe un faux binaire dans un dossier temporaire et
// branche l'Updater sur le faux GitHub.
func newTestUpdater(t *testing.T, fake *fakeGitHub, installed []byte) *testUpdater {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "opencloud")
	if err := os.WriteFile(executable, installed, executableMode); err != nil {
		t.Fatal(err)
	}
	return newTestUpdaterOn(t, fake.server.URL, executable)
}

// newTestUpdaterOn : un self-update de plus sur un binaire déjà installé.
func newTestUpdaterOn(t *testing.T, baseURL, executable string) *testUpdater {
	t.Helper()
	client := NewClient("", "test")
	client.baseURL = baseURL
	test := &testUpdater{out: &bytes.Buffer{}, verifier: &fakeVerifier{}, restart: &fakeRestart{}, executable: executable}
	test.updater = New(client, test.out)
	test.updater.verifier = test.verifier
	test.updater.restart = test.restart.restart
	return test
}

func (u *testUpdater) run(t *testing.T, current, requested string, checkOnly bool) (Result, error) {
	t.Helper()
	return u.updater.Run(context.Background(), Options{
		CurrentVersion:   current,
		ExecutablePath:   u.executable,
		RequestedVersion: requested,
		CheckOnly:        checkOnly,
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- chemin du test
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func expectRefusal(t *testing.T, err error, expectedWords ...string) refusal.Refusal {
	t.Helper()
	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	for _, word := range expectedWords {
		if !strings.Contains(refused.Error(), word) {
			t.Fatalf("le refus doit dire %q :\n%s", word, refused.Error())
		}
	}
	return refused
}

func TestRun_NextPatch_ReplacesBinaryKeepsPreviousAndRestarts(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	fake.attest([]byte("new binary"), `{"pretend":"bundle"}`)
	test := newTestUpdater(t, fake, []byte("old binary"))

	result, err := test.run(t, "0.0.2", "", false)

	if err != nil {
		t.Fatalf("erreur inattendue : %v\n%s", err, test.out.String())
	}
	if !result.Updated || !result.Restarted || result.Version.String() != "0.0.3" {
		t.Fatalf("résultat = %+v", result)
	}
	if got := readFile(t, test.executable); got != "new binary" {
		t.Fatalf("binaire en place = %q", got)
	}
	if got := readFile(t, test.executable+previousSuffix); got != "old binary" {
		t.Fatalf(".prev = %q", got)
	}
	info, _ := os.Stat(test.executable)
	if info.Mode().Perm() != executableMode {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if _, err := os.Stat(test.executable + pendingSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("le .new ne doit pas rester")
	}
	if test.restart.calls != 1 {
		t.Fatalf("redémarrages = %d", test.restart.calls)
	}
	wantSAN := "https://github.com/ldesfontaine/opencloud/.github/workflows/release.yml@refs/tags/v0.0.3"
	if test.verifier.identity.SubjectAlternativeName.SubjectAlternativeName != wantSAN || len(test.verifier.bundles) != 1 {
		t.Fatalf("le vérificateur a reçu %+v et %d bundle(s)", test.verifier.identity, len(test.verifier.bundles))
	}
	for _, line := range []string{"somme SHA-256     : vérifiée", "attestation       : vérifiée", "retour arrière", "redémarrée"} {
		if !strings.Contains(test.out.String(), line) {
			t.Fatalf("la sortie doit dire %q :\n%s", line, test.out.String())
		}
	}
}

func TestRun_MinorJump_IsRefusedNamingTheStep(t *testing.T) {
	fake := newFakeGitHub(t)
	for _, tag := range []string{"v0.2.1", "v0.2.4", "v0.3.0"} {
		fake.addRelease(tag, []byte(tag))
	}
	test := newTestUpdater(t, fake, []byte("old binary"))

	_, err := test.run(t, "0.1.0", "", false)

	expectRefusal(t, err, "saute", "--version v0.2.4")
	if readFile(t, test.executable) != "old binary" || test.restart.calls != 0 {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_MajorChange_IsRefusedWithoutNamingAStep(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v1.0.0", []byte("v1.0.0"))
	test := newTestUpdater(t, fake, []byte("old binary"))

	_, err := test.run(t, "0.9.0", "", false)

	refused := expectRefusal(t, err, "changement de version majeure", "pas pris en charge par self-update")
	if strings.Contains(refused.Remedy, "self-update --version") {
		t.Errorf("aucune étape à nommer pour une majeure : %q", refused.Remedy)
	}
	if readFile(t, test.executable) != "old binary" || test.restart.calls != 0 {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_ChecksumMismatch_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	release := fake.releases["v0.0.3"]
	release.assets[checksumsAssetName] = []byte(strings.Repeat("00", 32) + "  opencloud_0.0.3_linux_amd64\n")
	test := newTestUpdater(t, fake, []byte("old binary"))

	_, err := test.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "SHA-256", "ne correspond pas")
	if readFile(t, test.executable) != "old binary" {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_NoAttestation_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	test := newTestUpdater(t, fake, []byte("old binary"))

	_, err := test.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "aucune attestation")
	if readFile(t, test.executable) != "old binary" {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_InvalidAttestation_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	fake.attest([]byte("new binary"), `{"pretend":"bundle"}`)
	test := newTestUpdater(t, fake, []byte("old binary"))
	test.verifier.err = errors.New("signed by someone else")

	_, err := test.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "n'est pas valide", "signed by someone else")
	if readFile(t, test.executable) != "old binary" {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_AlreadyUpToDate_DoesNothing(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.2", []byte("same binary"))
	test := newTestUpdater(t, fake, []byte("same binary"))

	result, err := test.run(t, "0.0.2", "", false)

	if err != nil || result.Updated {
		t.Fatalf("résultat = %+v, err %v", result, err)
	}
	if !strings.Contains(test.out.String(), "déjà à jour") {
		t.Fatalf("la sortie doit le dire :\n%s", test.out.String())
	}
}

func TestRun_DevelopmentBuild_IsRefusedBeforeAnyRequest(t *testing.T) {
	fake := newFakeGitHub(t)
	test := newTestUpdater(t, fake, []byte("dev binary"))

	_, err := test.run(t, "0.0.1-3-gabc-dirty", "", false)

	expectRefusal(t, err, "n'est pas une release")
	if len(fake.requests) != 0 {
		t.Fatal("aucune requête ne doit partir pour un build de développement")
	}
}

func TestRun_CheckOnly_ChangesNothing(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	test := newTestUpdater(t, fake, []byte("old binary"))

	result, err := test.run(t, "0.0.2", "", true)

	if err != nil || result.Updated || result.Version.String() != "0.0.3" {
		t.Fatalf("résultat = %+v, err %v", result, err)
	}
	if readFile(t, test.executable) != "old binary" || test.restart.calls != 0 {
		t.Fatal("--check ne touche à rien")
	}
	if !strings.Contains(test.out.String(), "rien n'est modifié") {
		t.Fatalf("la sortie doit le dire :\n%s", test.out.String())
	}
}

func TestRun_RequestedOlderVersion_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.1", []byte("older"))
	test := newTestUpdater(t, fake, []byte("current"))

	_, err := test.run(t, "0.0.2", "v0.0.1", false)

	expectRefusal(t, err, "plus ancienne")
}

func TestRun_UnknownRequestedVersion_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	test := newTestUpdater(t, fake, []byte("current"))

	_, err := test.run(t, "0.0.2", "v9.9.9", false)
	expectRefusal(t, err, "v9.9.9 n'existe pas")

	_, err = test.run(t, "0.0.2", "latest", false)
	expectRefusal(t, err, "n'est pas une version X.Y.Z")
}

func TestRun_PendingFile_IsRefusedAsUpdateInProgress(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	fake.attest([]byte("new binary"), `{"pretend":"bundle"}`)
	test := newTestUpdater(t, fake, []byte("old binary"))
	if err := os.WriteFile(test.executable+pendingSuffix, []byte("half written"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := test.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "déjà en cours")
	if readFile(t, test.executable) != "old binary" || test.restart.calls != 0 {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_ConcurrentUpdates_SecondIsRefusedAndPreviousStaysTheOldBinary(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	fake.attest([]byte("new binary"), `{"pretend":"bundle"}`)
	first := newTestUpdater(t, fake, []byte("old binary"))

	// Le premier reste dans le téléchargement le temps que le second essaie.
	downloading := make(chan struct{})
	secondFinished := make(chan struct{})
	releaseFirst := sync.OnceFunc(func() { close(secondFinished) })
	t.Cleanup(releaseFirst)
	var reached sync.Once
	fake.beforeAsset = func(name string) {
		if name == checksumsAssetName {
			return
		}
		reached.Do(func() {
			close(downloading)
			<-secondFinished
		})
	}

	firstResult := make(chan error, 1)
	go func() {
		_, err := first.run(t, "0.0.2", "", false)
		firstResult <- err
	}()
	<-downloading

	// Le second parle à un GitHub qui refuse de répondre : le verrou doit
	// l'arrêter avant le premier appel.
	silent := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("le second self-update ne doit rien demander, reçu %s", r.URL.Path)
	}))
	t.Cleanup(silent.Close)
	second := newTestUpdaterOn(t, silent.URL, first.executable)

	_, err := second.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "déjà en cours", first.executable+lockSuffix)
	releaseFirst()
	if err := <-firstResult; err != nil {
		t.Fatalf("le premier doit aboutir : %v\n%s", err, first.out.String())
	}
	if got := readFile(t, first.executable); got != "new binary" {
		t.Fatalf("binaire en place = %q", got)
	}
	if got := readFile(t, first.executable+previousSuffix); got != "old binary" {
		t.Fatalf(".prev = %q : le retour arrière doit ramener l'ancien binaire", got)
	}
	if _, err := os.Stat(first.executable + lockSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("le verrou doit être rendu une fois la mise à jour finie")
	}
}

func TestRun_CheckOnly_DoesNotTakeTheLock(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	test := newTestUpdater(t, fake, []byte("old binary"))

	if _, err := test.run(t, "0.0.2", "", true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(test.executable + lockSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("--check ne modifie rien, il n'a pas à verrouiller")
	}
}

func TestRun_LeftoverLock_IsRefusedBeforeAnyRequest(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	test := newTestUpdater(t, fake, []byte("old binary"))
	if err := os.WriteFile(test.executable+lockSuffix, nil, lockFileMode); err != nil {
		t.Fatal(err)
	}

	_, err := test.run(t, "0.0.2", "", false)

	expectRefusal(t, err, "déjà en cours", "retirer")
	if len(fake.requests) != 0 {
		t.Fatal("un verrou pris arrête avant le premier appel à GitHub")
	}
}

func TestRun_RequestedPrerelease_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.2", []byte("current"))
	fake.addPrerelease("v0.0.3", []byte("not for you"))
	test := newTestUpdater(t, fake, []byte("current"))

	_, err := test.run(t, "0.0.2", "v0.0.3", false)

	expectRefusal(t, err, "préversion")
	if readFile(t, test.executable) != "current" {
		t.Fatal("un refus ne touche à rien")
	}
}

func TestRun_WithoutSystemd_SaysSoAndStillSucceeds(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("new binary"))
	fake.attest([]byte("new binary"), `{"pretend":"bundle"}`)
	test := newTestUpdater(t, fake, []byte("old binary"))
	test.restart.err = ErrNoSystemd

	result, err := test.run(t, "0.0.2", "", false)

	if err != nil || !result.Updated || result.Restarted {
		t.Fatalf("résultat = %+v, err %v", result, err)
	}
	if !strings.Contains(test.out.String(), "pas de systemd") {
		t.Fatalf("la sortie doit le dire :\n%s", test.out.String())
	}
}

func TestRun_PrivateRepositoryWithoutToken_IsRefusedNamingTheTokenFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(forbidden.Close)
	client := NewClient("", "test")
	client.baseURL = forbidden.URL
	updater := New(client, &bytes.Buffer{})

	_, err := updater.Run(context.Background(), Options{CurrentVersion: "0.0.2", ExecutablePath: filepath.Join(t.TempDir(), "opencloud")})

	expectRefusal(t, err, TokenPath, "0600 root:root")
}

func TestDownloadAsset_URLOutsideTheRepository_IsRefused(t *testing.T) {
	fake := newFakeGitHub(t)
	client := NewClient("", "test")
	client.baseURL = fake.server.URL

	elsewhere := Asset{Name: "opencloud_0.0.3_linux_amd64", URL: "https://example.invalid/binary", Size: 10}
	if _, _, err := client.DownloadAsset(context.Background(), elsewhere, maxBinaryBytes); !errors.Is(err, ErrURLOutsideRepository) {
		t.Fatalf("une autre adresse doit être refusée, reçu %v", err)
	}
	otherRepository := Asset{Name: "opencloud_0.0.3_linux_amd64", URL: fake.server.URL + "/repos/someone/else/releases/assets/1", Size: 10}
	if _, _, err := client.DownloadAsset(context.Background(), otherRepository, maxBinaryBytes); !errors.Is(err, ErrURLOutsideRepository) {
		t.Fatalf("un autre dépôt doit être refusé, reçu %v", err)
	}
	if len(fake.requests) != 0 {
		t.Fatal("rien ne doit partir vers une adresse hors de l'API du dépôt")
	}
}

func TestFetchAttestations_BundleURLOutsideTheRepository_IsRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("un bundle hors de l'API du dépôt ne doit pas être téléchargé")
	}))
	t.Cleanup(elsewhere.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"attestations": []map[string]any{{"bundle_url": elsewhere.URL + "/bundle.json"}}})
	}))
	t.Cleanup(api.Close)
	client := NewClient("", "test")
	client.baseURL = api.URL

	_, err := client.FetchAttestations(context.Background(), sha256.Sum256([]byte("new binary")))

	if !errors.Is(err, ErrURLOutsideRepository) {
		t.Fatalf("attendu un refus de l'adresse, reçu %v", err)
	}
}

func TestClient_SendsTokenAndAPIHeaders(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.addRelease("v0.0.3", []byte("binary"))
	client := NewClient("github_pat_secret", "opencloud/test")
	client.baseURL = fake.server.URL

	if _, err := client.LatestRelease(context.Background()); err != nil {
		t.Fatal(err)
	}

	request := fake.requests[0]
	if request.Header.Get("Authorization") != "Bearer github_pat_secret" ||
		request.Header.Get("X-GitHub-Api-Version") != githubAPIVersion ||
		request.Header.Get("User-Agent") != "opencloud/test" ||
		request.Header.Get("Accept") != "application/vnd.github+json" {
		t.Fatalf("en-têtes = %v", request.Header)
	}
}
