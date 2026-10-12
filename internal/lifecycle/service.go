package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/registry"
)

const (
	// RefreshEvery : la source bouge quelques fois par semaine.
	RefreshEvery = 24 * time.Hour
	// La boucle regarde toutes les heures si une lecture est due. Un essai
	// raté se rejoue à la passe suivante, pas le lendemain.
	checkPeriod = time.Hour
	retryAfter  = checkPeriod / 2
	// Une pause entre deux produits : la source est gratuite, on ne la
	// presse pas.
	pauseBetweenProducts = 500 * time.Millisecond
	// Ce qu'une lecture demande au plus à la source.
	maxProducts = 64
)

// Images rend les images des services présents ; le câblage la tire des
// fiches.
type Images func(ctx context.Context) ([]string, error)

// Systems rend le système de chaque machine, tel que son agent le nomme.
type Systems func(ctx context.Context) ([]string, error)

// Cache garde la dernière lecture d'un démarrage à l'autre.
type Cache interface {
	// Load rend ce que Save a écrit ; nil tant que rien ne l'a été.
	Load() ([]byte, error)
	Save(payload []byte) error
}

// Watcher est prévenu après chaque essai de lecture : le direct s'y
// branche, les fiches ouvertes et Paramètres se relisent. nil est toléré.
type Watcher interface {
	ServicesChanged(machineID string)
}

// Service lit la source quand le réglage le permet, garde la dernière
// lecture, et répond sans jamais sortir sur le réseau.
type Service struct {
	images  Images
	systems Systems
	cache   Cache
	source  Source
	enabled func() bool
	watcher Watcher
	logger  *slog.Logger
	now     func() time.Time
	pause   time.Duration
	kick    chan struct{}

	mu       sync.RWMutex
	snapshot Snapshot
	// Les produits demandés à la dernière lecture, qu'ils aient répondu ou
	// non : un produit qui ne répond pas ne relance pas une lecture.
	asked         map[string]bool
	isFailing     bool
	isForced      bool
	lastAttemptAt time.Time
}

// New : enabled est relu à chaque passage, un réglage changé sous
// Paramètres vaut aussitôt.
func New(images Images, systems Systems, cache Cache, source Source, enabled func() bool, logger *slog.Logger) *Service {
	return &Service{
		images: images, systems: systems, cache: cache, source: source, enabled: enabled, logger: logger,
		now: time.Now, pause: pauseBetweenProducts, kick: make(chan struct{}, 1),
	}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

// SetPause règle l'attente entre deux produits ; les tests la mettent à zéro.
func (s *Service) SetPause(pause time.Duration) {
	s.pause = pause
}

// Load relit la dernière lecture gardée : après un redémarrage, les fiches
// répondent sans attendre la source.
func (s *Service) Load() error {
	payload, err := s.cache.Load()
	if err != nil {
		return err
	}
	if payload == nil {
		return nil
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return fmt.Errorf("decode lifecycle snapshot: %w", err)
	}
	s.mu.Lock()
	s.snapshot = snapshot
	s.asked = make(map[string]bool, len(snapshot.Products))
	for name := range snapshot.Products {
		s.asked[name] = true
	}
	s.mu.Unlock()
	return nil
}

// Watch relit la dernière lecture gardée, puis lit la source quand une
// lecture est due, quand une image apporte un produit jamais demandé, ou
// tout de suite quand Kick le demande.
func (s *Service) Watch(ctx context.Context) {
	if err := s.Load(); err != nil {
		s.logger.Warn("load lifecycle snapshot", "error", err)
	}
	ticker := time.NewTicker(checkPeriod)
	defer ticker.Stop()
	for {
		if s.enabled() && (s.isDue() || s.hasNewProduct(ctx)) {
			if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("refresh lifecycle", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.kick:
		}
	}
}

// Kick demande une lecture sans attendre la cadence : le réglage vient
// d'être activé. Sans effet de plus si une demande attend déjà.
func (s *Service) Kick() {
	s.mu.Lock()
	s.isForced = true
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// isDue : demandée par Kick, ou la dernière lecture a un jour, ou le
// dernier essai a raté et la passe suivante est là.
func (s *Service) isDue() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.isForced {
		return true
	}
	if s.isFailing {
		return s.now().Sub(s.lastAttemptAt) >= retryAfter
	}
	return s.now().Sub(s.snapshot.FetchedAt) >= RefreshEvery
}

// hasNewProduct : un service ou une machine arrivés depuis la dernière
// lecture apportent un produit qu'on n'a pas encore demandé. Leur fiche
// n'attend pas demain.
func (s *Service) hasNewProduct(ctx context.Context) bool {
	images, systems, err := s.inUse(ctx)
	if err != nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, name := range productsOf(images, systems, s.snapshot.Repositories) {
		if !s.asked[name] {
			return true
		}
	}
	return false
}

// inUse rend ce dont on veut connaître le support : les images des
// services et les systèmes des machines.
func (s *Service) inUse(ctx context.Context) (images, systems []string, err error) {
	images, err = s.images(ctx)
	if err != nil {
		return nil, nil, err
	}
	systems, err = s.systems(ctx)
	if err != nil {
		return nil, nil, err
	}
	return images, systems, nil
}

// Refresh lit la source : quel dépôt est quel produit, puis les branches
// des produits que les services et les machines utilisent. Un produit qui ne répond pas
// garde ses branches de la lecture précédente. Sans le réglage, rien ne
// sort.
func (s *Service) Refresh(ctx context.Context) error {
	if !s.enabled() {
		return nil
	}
	now := s.now()
	snapshot, wanted, err := s.fetch(ctx, now)
	s.mu.Lock()
	s.lastAttemptAt = now
	s.isForced = false
	s.isFailing = err != nil
	if err == nil {
		s.snapshot = snapshot
		s.asked = make(map[string]bool, len(wanted))
		for _, name := range wanted {
			s.asked[name] = true
		}
	}
	s.mu.Unlock()
	// Réussi ou non, l'essai se voit : Paramètres relit l'état, les fiches
	// ouvertes se relisent.
	if s.watcher != nil {
		s.watcher.ServicesChanged("")
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode lifecycle snapshot: %w", err)
	}
	if err := s.cache.Save(payload); err != nil {
		return err
	}
	s.logger.Info("lifecycle refreshed", "repositories", len(snapshot.Repositories), "products", len(snapshot.Products))
	return nil
}

// fetch rend la lecture et les produits qu'elle a demandés.
func (s *Service) fetch(ctx context.Context, now time.Time) (Snapshot, []string, error) {
	images, systems, err := s.inUse(ctx)
	if err != nil {
		return Snapshot{}, nil, err
	}
	repositories, err := s.source.Repositories(ctx)
	if err != nil {
		return Snapshot{}, nil, err
	}
	s.mu.RLock()
	previous := s.snapshot.Products
	s.mu.RUnlock()
	snapshot := Snapshot{FetchedAt: now.UTC(), Repositories: repositories, Products: make(map[string]Product), NotFound: make(map[string]bool)}
	wanted := productsOf(images, systems, repositories)
	failed := 0
	for index, name := range wanted {
		if index > 0 {
			if err := sleep(ctx, s.pause); err != nil {
				return Snapshot{}, nil, err
			}
		}
		product, err := s.source.Product(ctx, name)
		if errors.Is(err, ErrNoProduct) {
			// La source n'a pas ce produit : pour la fiche, c'est un
			// produit qu'elle ne suit pas, pas une panne.
			snapshot.NotFound[name] = true
			continue
		}
		if err != nil {
			s.logger.Warn("read lifecycle product", "product", name, "error", err)
			failed++
			if kept, ok := previous[name]; ok {
				snapshot.Products[name] = kept
			}
			continue
		}
		product.FetchedAt = snapshot.FetchedAt
		snapshot.Products[name] = product
	}
	// Aucun produit n'a répondu : la source est en panne, pas un produit.
	// On garde la lecture précédente et on réessaie à la passe suivante.
	if failed > 0 && failed == len(wanted) {
		return Snapshot{}, nil, fmt.Errorf("%w: no product answered", ErrSource)
	}
	return snapshot, wanted, nil
}

// productsOf rend les produits à lire : ceux des images et des systèmes
// présents, dans un ordre stable, bornés.
func productsOf(images, systems []string, repositories map[string]string) []string {
	wanted := make(map[string]bool)
	for _, image := range images {
		reference, err := registry.Parse(image)
		if err != nil {
			continue
		}
		if name, ok := repositories[reference.Repository]; ok {
			wanted[name] = true
		}
	}
	for _, system := range systems {
		if name, _, ok := systemOf(system); ok {
			wanted[name] = true
		}
	}
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxProducts {
		names = names[:maxProducts]
	}
	return names
}

func sleep(ctx context.Context, pause time.Duration) error {
	if pause <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Of rend le support d'une image d'après la dernière lecture gardée, sans
// rien demander à la source. C'est à l'appelant de ne pas l'afficher quand
// le réglage est coupé.
func (s *Service) Of(image string) Verdict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return verdictOf(s.snapshot, image, s.now())
}

// OfSystem rend le support du système d'une machine, de la même lecture.
func (s *Service) OfSystem(system string) Verdict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return verdictOfSystem(s.snapshot, system, s.now())
}

func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return State{
		FetchedAt: s.snapshot.FetchedAt, Products: len(s.snapshot.Products),
		IsFailing: s.isFailing, LastAttemptAt: s.lastAttemptAt,
	}
}
