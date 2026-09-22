package update

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

const (
	// Le premier passage attend que Docker et les conteneurs soient là.
	FirstDelay = 2 * time.Minute
	// Une image se revérifie une fois par jour : une image en retard se
	// voit au plus un jour après sa publication, et le quota du Hub reste
	// loin.
	Interval = 24 * time.Hour
	// Entre deux, un tour léger sur la socket Docker attrape une image
	// nouvelle sur la machine et la vérifie sans attendre le lendemain.
	DiscoveryInterval = 5 * time.Minute
	// Une pause entre deux images : pas de rafale sur un registre.
	PauseBetweenImages = 2 * time.Second
)

// ContainerLister est ce que la boucle demande à Docker : les conteneurs
// de la machine, pour en tirer les images distinctes.
type ContainerLister interface {
	ListContainers(ctx context.Context) ([]dockerapi.Summary, error)
}

// ImageChecker vérifie une image ; Checker le fait, un faux dans les tests.
type ImageChecker interface {
	Check(ctx context.Context, image string, now time.Time) (Result, error)
}

// Sink reçoit ce qu'un passage a constaté : le tampon de l'agent, qui le
// livre avec le signal, ou directement le composant sur la machine
// openCloud.
type Sink interface {
	Deliver(Report)
}

type timing struct {
	first, discovery, interval, pause time.Duration
}

// Runner est la boucle de l'agent : elle liste les images des
// conteneurs de la machine, vérifie celles qui sont dues, et livre.
type Runner struct {
	docker  ContainerLister
	checker ImageChecker
	sink    Sink
	logger  *slog.Logger
	now     func() time.Time
	timing  timing
	wake    chan struct{}

	mu       sync.Mutex
	excluded map[string]bool
	checked  map[string]time.Time
}

func NewRunner(docker ContainerLister, checker ImageChecker, sink Sink, logger *slog.Logger) *Runner {
	return &Runner{
		docker: docker, checker: checker, sink: sink, logger: logger, now: time.Now,
		timing:   timing{first: FirstDelay, discovery: DiscoveryInterval, interval: Interval, pause: PauseBetweenImages},
		wake:     make(chan struct{}, 1),
		excluded: make(map[string]bool),
		checked:  make(map[string]time.Time),
	}
}

// Assign remplace la liste des images exclues ; « maintenant » lance un
// passage complet sans attendre.
func (r *Runner) Assign(assignment Assignment) {
	r.mu.Lock()
	r.excluded = make(map[string]bool, len(assignment.Excluded))
	for _, image := range assignment.Excluded {
		r.excluded[image] = true
	}
	r.mu.Unlock()
	if assignment.Now {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// Run tient la boucle jusqu'à ce que le contexte s'arrête.
func (r *Runner) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(r.timing.first):
		r.pass(ctx, false)
	case <-r.wake:
		r.pass(ctx, true)
	}
	ticker := time.NewTicker(r.timing.discovery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pass(ctx, false)
		case <-r.wake:
			r.pass(ctx, true)
		}
	}
}

// pass vérifie chaque image due : jamais vue, ou vue il y a plus d'un
// jour ; toutes quand c'est demandé. Une image dont Docker ne dit rien
// reste due pour le tour suivant.
func (r *Runner) pass(ctx context.Context, everything bool) {
	images, err := r.images(ctx)
	if err != nil {
		r.logger.Debug("list images to check", "error", err)
		return
	}
	var results []Result
	for index, image := range images {
		if ctx.Err() != nil {
			return
		}
		if !everything && !r.isDue(image) {
			continue
		}
		if index > 0 && !r.rest(ctx) {
			return
		}
		result, err := r.checker.Check(ctx, image, r.now())
		if err != nil {
			r.logger.Debug("check image", "image", image, "error", err)
			continue
		}
		r.mu.Lock()
		r.checked[image] = result.CheckedAt
		r.mu.Unlock()
		results = append(results, result)
	}
	if len(results) > 0 {
		r.sink.Deliver(Report{Results: results})
	}
}

// images rend les images distinctes des conteneurs de la machine, dans
// l'ordre de la liste, sans les jetables ni les exclues.
func (r *Runner) images(ctx context.Context) ([]string, error) {
	containers, err := r.docker.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(containers))
	var images []string
	for _, container := range containers {
		image := container.Image
		if image == "" || seen[image] || container.IsOneOff() || r.excluded[image] {
			continue
		}
		seen[image] = true
		images = append(images, image)
	}
	return images, nil
}

func (r *Runner) isDue(image string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last, seen := r.checked[image]
	return !seen || r.now().Sub(last) >= r.timing.interval
}

func (r *Runner) rest(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(r.timing.pause):
		return true
	}
}
