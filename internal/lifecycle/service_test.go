package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	repositories    map[string]string
	products        map[string]Product
	repositoriesErr error
	failing         map[string]bool
	asked           []string
	listed          int
}

func (f *fakeSource) Repositories(context.Context) (map[string]string, error) {
	f.listed++
	if f.repositoriesErr != nil {
		return nil, f.repositoriesErr
	}
	// Une copie : la lecture en retire ce que la source n'a pas.
	repositories := make(map[string]string, len(f.repositories))
	for repository, name := range f.repositories {
		repositories[repository] = name
	}
	return repositories, nil
}

func (f *fakeSource) Product(_ context.Context, name string) (Product, error) {
	f.asked = append(f.asked, name)
	if f.failing[name] {
		return Product{}, ErrSource
	}
	product, known := f.products[name]
	if !known {
		return Product{}, fmt.Errorf("%w: %w", ErrSource, ErrNoProduct)
	}
	return product, nil
}

type memoryCache struct {
	payload []byte
	saves   int
}

func (m *memoryCache) Load() ([]byte, error) { return m.payload, nil }

func (m *memoryCache) Save(payload []byte) error {
	m.payload = payload
	m.saves++
	return nil
}

type countingWatcher struct{ count int }

func (c *countingWatcher) ContainersChanged(string) { c.count++ }

type harness struct {
	service *Service
	source  *fakeSource
	cache   *memoryCache
	watcher *countingWatcher
	now     time.Time
	enabled atomic.Bool
	images  []string
	systems []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sample := sampleSnapshot()
	h := &harness{
		source:  &fakeSource{repositories: sample.Repositories, products: sample.Products, failing: map[string]bool{}},
		cache:   &memoryCache{},
		watcher: &countingWatcher{},
		now:     fetchedAt,
		images:  []string{"postgres:16.4", "traefik:v3.1.4", "postgres:17", "vaultwarden/server:1.32.0", "not an image"},
	}
	h.enabled.Store(true)
	images := func(context.Context) ([]string, error) { return h.images, nil }
	systems := func(context.Context) ([]string, error) { return h.systems, nil }
	h.service = New(images, systems, h.cache, h.source, h.enabled.Load, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.service.SetClock(func() time.Time { return h.now })
	h.service.SetWatcher(h.watcher)
	h.service.SetPause(0)
	return h
}

func TestRefresh_Disabled_NeverCallsTheSource(t *testing.T) {
	h := newHarness(t)
	h.enabled.Store(false)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.source.listed != 0 || len(h.source.asked) != 0 || h.cache.saves != 0 || h.watcher.count != 0 {
		t.Fatalf("the source was read while the setting is off: %+v", h.source)
	}
	if got := h.service.Of("postgres:16.4"); got.Status != StatusPending {
		t.Fatalf("verdict %+v", got)
	}
}

func TestRefresh_ReadsOnlyTheProductsInUse(t *testing.T) {
	h := newHarness(t)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.source.asked) != 2 || h.source.asked[0] != "postgresql" || h.source.asked[1] != "traefik" {
		t.Fatalf("asked %v", h.source.asked)
	}
	if got := h.service.Of("postgres:16.4"); got.Status != StatusMaintained || got.EOLFrom != "2028-11-09" {
		t.Fatalf("verdict %+v", got)
	}
	if got := h.service.Of("traefik:v3.1.4"); got.Status != StatusEOL {
		t.Fatalf("verdict %+v", got)
	}
	state := h.service.State()
	if state.Products != 2 || state.IsFailing || !state.FetchedAt.Equal(fetchedAt) || !state.LastAttemptAt.Equal(fetchedAt) {
		t.Fatalf("state %+v", state)
	}
	if h.cache.saves != 1 || h.watcher.count != 1 {
		t.Fatalf("saves %d, notifications %d", h.cache.saves, h.watcher.count)
	}
}

// Les systèmes des machines se lisent dans la même passe que les images,
// sans rien demander pour un système que la table ne connaît pas.
func TestRefresh_ReadsTheSystemsOfTheMachinesToo(t *testing.T) {
	h := newHarness(t)
	h.images = nil
	h.systems = []string{"Debian GNU/Linux 12", "Debian GNU/Linux 11", "Arch Linux", "linux", ""}
	h.source.products["debian"] = Product{Name: "debian", Label: "Debian", Cycles: []Cycle{
		{Name: "13", IsMaintained: true, EOLFrom: "2030-06-30", Latest: "13.7"},
		{Name: "12", IsMaintained: true, EOLFrom: "2028-06-30", Latest: "12.15"},
		{Name: "11", IsEOL: true, IsMaintained: true, EOLFrom: "2026-08-31", Latest: "11.11"},
	}}
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.source.asked) != 1 || h.source.asked[0] != "debian" {
		t.Fatalf("asked %v", h.source.asked)
	}
	current := h.service.OfSystem("Debian GNU/Linux 12")
	if current.Status != StatusMaintained || current.Product != "Debian" || current.Cycle != "12" || current.EOLFrom != "2028-06-30" || current.Latest != "12.15" {
		t.Fatalf("debian 12: %+v", current)
	}
	if len(current.Newer) != 1 || current.Newer[0].Cycle != "13" || current.ProductURL != "https://endoflife.date/debian" {
		t.Fatalf("debian 12: %+v", current)
	}
	// La source dit encore « maintenue » d'une branche finie, pour son
	// support payant : c'est la fin de support qui compte.
	if old := h.service.OfSystem("Debian GNU/Linux 11"); old.Status != StatusEOL || len(old.Newer) != 2 {
		t.Fatalf("debian 11: %+v", old)
	}
	for _, system := range []string{"Arch Linux", "linux", "", "Windows Server 2022"} {
		if got := h.service.OfSystem(system); got.Status != StatusUnknownProduct {
			t.Errorf("%q: %+v", system, got)
		}
	}
	if h.service.hasNewProduct(context.Background()) {
		t.Fatal("nothing new")
	}
	h.systems = append(h.systems, "Ubuntu 24.04")
	if !h.service.hasNewProduct(context.Background()) {
		t.Fatal("a machine on a system never asked: due")
	}
}

func TestRefresh_SourceDown_KeepsThePreviousReading(t *testing.T) {
	h := newHarness(t)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.now = fetchedAt.Add(RefreshEvery)
	h.source.repositoriesErr = ErrSource
	if err := h.service.Refresh(context.Background()); !errors.Is(err, ErrSource) {
		t.Fatalf("error %v", err)
	}
	state := h.service.State()
	if !state.IsFailing || !state.FetchedAt.Equal(fetchedAt) || !state.LastAttemptAt.Equal(h.now) {
		t.Fatalf("state %+v", state)
	}
	if got := h.service.Of("postgres:16.4"); got.Status != StatusMaintained {
		t.Fatalf("verdict %+v", got)
	}
	// L'essai raté n'est pas gardé, mais il se voit : Paramètres se relit.
	if h.cache.saves != 1 || h.watcher.count != 2 {
		t.Fatalf("saves %d, notifications %d", h.cache.saves, h.watcher.count)
	}
}

func TestRefresh_OneProductDown_KeepsItsPreviousBranches(t *testing.T) {
	h := newHarness(t)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.now = fetchedAt.Add(StaleAfter + time.Hour)
	h.source.failing["traefik"] = true
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Ses branches restent, avec leur âge : la fiche dit qu'elles datent.
	if got := h.service.Of("traefik:v3.1.4"); got.Status != StatusEOL || !got.FetchedAt.Equal(fetchedAt) || !got.IsStale {
		t.Fatalf("verdict %+v", got)
	}
	if got := h.service.Of("postgres:16.4"); !got.FetchedAt.Equal(h.now) || got.IsStale {
		t.Fatalf("verdict %+v", got)
	}
	if h.service.State().IsFailing {
		t.Fatal("one product down is not the source down")
	}
}

// La source annonce un dépôt sans avoir son produit : ce n'est pas une
// panne, et la fiche dit « support inconnu » plutôt que d'attendre.
func TestRefresh_ProductTheSourceDoesNotHave_IsUnknownNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.images = []string{"redis:7.4"}
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.service.Of("redis:7.4"); got.Status != StatusUnknownProduct {
		t.Fatalf("verdict %+v", got)
	}
	if state := h.service.State(); state.IsFailing || state.FetchedAt.IsZero() {
		t.Fatalf("state %+v", state)
	}
	if h.service.hasNewProduct(context.Background()) {
		t.Fatal("a product the source does not have is not asked again every pass")
	}
}

func TestRefresh_NoProductAnswers_IsAFailure(t *testing.T) {
	h := newHarness(t)
	h.source.failing["traefik"], h.source.failing["postgresql"] = true, true
	if err := h.service.Refresh(context.Background()); !errors.Is(err, ErrSource) {
		t.Fatalf("error %v", err)
	}
	if state := h.service.State(); !state.IsFailing || !state.FetchedAt.IsZero() {
		t.Fatalf("state %+v", state)
	}
}

func TestRefresh_NoKnownImage_StillSucceeds(t *testing.T) {
	h := newHarness(t)
	h.images = []string{"vaultwarden/server:1.32.0"}
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.service.Of("vaultwarden/server:1.32.0"); got.Status != StatusUnknownProduct {
		t.Fatalf("verdict %+v", got)
	}
}

func TestLoad_ReadsBackWhatRefreshKept(t *testing.T) {
	h := newHarness(t)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := newHarness(t)
	restarted.cache.payload = h.cache.payload
	if err := restarted.service.Load(); err != nil {
		t.Fatal(err)
	}
	if got := restarted.service.Of("postgres:16.4"); got.Status != StatusMaintained || got.Cycle != "16" || len(got.Newer) != 2 {
		t.Fatalf("verdict %+v", got)
	}
	if restarted.source.listed != 0 {
		t.Fatal("loading must not read the source")
	}
	restarted.cache.payload = []byte("{broken")
	if err := restarted.service.Load(); err == nil {
		t.Fatal("a broken cache must be reported")
	}
}

// Une lecture par jour, deux au plus : jamais à moins de douze heures de
// la précédente, sauf quand l'opérateur vient d'activer le réglage.
func TestIsDue_OnceADayTwiceAtMost(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if !h.service.isDue(ctx) {
		t.Fatal("never read: due")
	}
	if err := h.service.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	h.now = fetchedAt.Add(RefreshEvery - time.Minute)
	if h.service.isDue(ctx) {
		t.Fatal("read less than a day ago, nothing new: not due")
	}
	h.now = fetchedAt.Add(RefreshEvery)
	if !h.service.isDue(ctx) {
		t.Fatal("read a day ago: due")
	}

	// Un essai raté ne se rejoue pas dans l'heure : douze heures après.
	h.source.repositoriesErr = ErrSource
	_ = h.service.Refresh(ctx)
	h.now = h.now.Add(MinGap - time.Minute)
	if h.service.isDue(ctx) {
		t.Fatal("failed less than twelve hours ago: wait")
	}
	h.now = h.now.Add(time.Minute)
	if !h.service.isDue(ctx) {
		t.Fatal("failed twelve hours ago: due")
	}
	h.source.repositoriesErr = nil
	if err := h.service.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	// Un produit jamais demandé attend lui aussi les douze heures.
	h.images = append(h.images, "redis:7.4")
	h.source.products["redis"] = Product{Name: "redis", Label: "Redis"}
	h.now = h.now.Add(MinGap - time.Minute)
	if h.service.isDue(ctx) {
		t.Fatal("a new product less than twelve hours after a reading: wait")
	}
	h.now = h.now.Add(time.Minute)
	if !h.service.isDue(ctx) {
		t.Fatal("a new product, twelve hours after: due")
	}

	// L'opérateur vient d'activer : tout de suite, quel que soit le délai.
	if err := h.service.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if h.service.isDue(ctx) {
		t.Fatal("just read: not due")
	}
	h.service.Kick()
	if !h.service.isDue(ctx) {
		t.Fatal("asked for: due")
	}
}

// Après un redémarrage, la lecture gardée compte : on ne relit pas la
// source parce que le processus vient de naître.
func TestIsDue_AfterARestart_TheKeptReadingCounts(t *testing.T) {
	h := newHarness(t)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := newHarness(t)
	restarted.cache.payload = h.cache.payload
	restarted.now = fetchedAt.Add(time.Hour)
	if err := restarted.service.Load(); err != nil {
		t.Fatal(err)
	}
	if restarted.service.isDue(context.Background()) {
		t.Fatal("read an hour ago by the previous process: not due")
	}
}

func TestHasNewProduct_OnlyForAProductNeverAsked(t *testing.T) {
	h := newHarness(t)
	h.images = []string{"postgres:16.4"}
	h.source.failing["traefik"] = true
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.service.hasNewProduct(context.Background()) {
		t.Fatal("nothing new")
	}
	h.images = []string{"postgres:16.4", "postgres:17", "vaultwarden/server:1.32.0"}
	if h.service.hasNewProduct(context.Background()) {
		t.Fatal("another tag of a known product, and an image the source does not follow: nothing to ask")
	}
	h.images = append(h.images, "traefik:v3.1.4")
	if !h.service.hasNewProduct(context.Background()) {
		t.Fatal("a product never asked: due")
	}
	// Demandé, sans réponse : on ne le redemande pas à chaque passe.
	h.now = h.now.Add(time.Hour)
	if err := h.service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.service.hasNewProduct(context.Background()) {
		t.Fatal("asked without an answer: wait for the daily reading")
	}
	if got := h.service.Of("traefik:v3.1.4"); got.Status != StatusPending {
		t.Fatalf("verdict %+v", got)
	}
}

func TestWatch_KickReadsWithoutWaitingForTheTicker(t *testing.T) {
	h := newHarness(t)
	h.enabled.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.service.Watch(ctx)
		close(done)
	}()
	h.enabled.Store(true)
	h.service.Kick()
	deadline := time.After(5 * time.Second)
	for h.service.State().FetchedAt.IsZero() {
		select {
		case <-deadline:
			t.Fatal("the kick did not trigger a reading")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

func TestFileCache_KeepsTheLastWrite(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cache := NewFileCache(root)
	if payload, err := cache.Load(); err != nil || payload != nil {
		t.Fatalf("empty cache: %q, %v", payload, err)
	}
	if err := cache.Save([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := cache.Save([]byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if payload, err := cache.Load(); err != nil || string(payload) != `{"a":2}` {
		t.Fatalf("cache: %q, %v", payload, err)
	}
}
