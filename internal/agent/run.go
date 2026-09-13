package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// ErrIdentityRefused : le serveur ne reconnaît plus cette machine ; seul un
// nouvel enrôlement la ramène.
var ErrIdentityRefused = errors.New("identity refused by the server")

const (
	DefaultSignalInterval = 30 * time.Second
	minBackoff            = time.Second
	maxBackoff            = time.Minute
	backoffJitter         = 0.25
	// Un flux qui a tenu ce temps remet le délai de reconnexion au plus court.
	stableStream = 30 * time.Second
)

type Options struct {
	StateDir *os.Root
	// Server et Token ne servent qu'au premier démarrage, pour s'enrôler.
	Server string
	Token  string
	Pin    string
	// AllowPlain accepte http:// vers un serveur distant ; à réserver à un
	// réseau déjà chiffré.
	AllowPlain bool
	// Language est la langue de l'opérateur, donnée par la commande
	// d'installation ; l'identité la garde ensuite.
	Language lang.Code
	// Version est celle du binaire ; le serveur l'affiche.
	Version        string
	SignalInterval time.Duration
	Logger         *slog.Logger
}

// Run enrôle l'agent si besoin, puis tient le flux ouvert jusqu'à ce que le
// contexte s'arrête ou que le serveur refuse l'identité pour de bon.
func Run(ctx context.Context, opts Options) error {
	if opts.SignalInterval <= 0 {
		opts.SignalInterval = DefaultSignalInterval
	}
	identity, err := loadOrEnroll(ctx, opts)
	if err != nil {
		return err
	}
	client, err := NewClient(identity.Server, identity.Pin, opts.Version, opts.AllowPlain)
	if err != nil {
		return err
	}
	return keepConnected(ctx, client, identity, opts)
}

func loadOrEnroll(ctx context.Context, opts Options) (Identity, error) {
	identity, err := LoadIdentity(opts.StateDir)
	if err == nil {
		opts.Logger.Info("identity loaded", "machine_id", identity.MachineID, "server", identity.Server)
		return identity, nil
	}
	if !errors.Is(err, ErrNotEnrolled) {
		return Identity{}, err
	}
	if opts.Server == "" || opts.Token == "" {
		return Identity{}, ErrNotEnrolled
	}
	return enroll(ctx, opts)
}

// enroll tire l'identité, la présente avec le jeton, puis l'écrit avec l'id
// que le serveur a retenu : un ré-enrôlement garde l'id de la machine.
func enroll(ctx context.Context, opts Options) (Identity, error) {
	identity, err := NewIdentity(opts.Server, opts.Pin, opts.Language)
	if err != nil {
		return Identity{}, err
	}
	client, err := NewClient(identity.Server, identity.Pin, opts.Version, opts.AllowPlain)
	if err != nil {
		return Identity{}, err
	}
	response, err := client.Enroll(ctx, identity, opts.Token, hostinfo.Collect())
	if err != nil {
		return Identity{}, err
	}
	identity.MachineID = response.MachineID
	identity.EnrolledAt = time.Now().UTC()
	if err := SaveIdentity(opts.StateDir, identity); err != nil {
		return Identity{}, err
	}
	opts.Logger.Info("enrolled", "machine_id", identity.MachineID, "name", response.Name)
	return identity, nil
}

func keepConnected(ctx context.Context, client *Client, identity Identity, opts Options) error {
	delay := minBackoff
	for {
		startedAt := time.Now()
		err := connectOnce(ctx, client, identity, opts)
		if ctx.Err() != nil {
			return nil
		}
		var refused *ServerError
		if errors.As(err, &refused) && refused.Permanent() {
			return fmt.Errorf("%w: %w", ErrIdentityRefused, err)
		}
		if time.Since(startedAt) > stableStream {
			delay = minBackoff
		}
		opts.Logger.Warn("stream lost, reconnecting", "error", err, "delay", delay.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jittered(delay)):
		}
		delay = min(delay*2, maxBackoff)
	}
}

// connectOnce tient un flux et envoie un signal à intervalle régulier tant
// qu'il vit ; il rend la main dès que le flux tombe.
func connectOnce(ctx context.Context, client *Client, identity Identity, opts Options) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.OpenStream(streamCtx, identity)
	if err != nil {
		return err
	}
	defer stream.Close()
	opts.Logger.Info("connected", "server", identity.Server)

	lost := make(chan error, 1)
	go func() { lost <- stream.Follow() }()

	ticker := time.NewTicker(opts.SignalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-lost:
			return err
		case <-ticker.C:
			if err := client.Signal(ctx, stream.Session); err != nil {
				return err
			}
		}
	}
}

func jittered(delay time.Duration) time.Duration {
	spread := float64(delay) * backoffJitter
	offset := (rand.Float64()*2 - 1) * spread // #nosec G404 -- un délai de reconnexion, pas un secret.
	return delay + time.Duration(offset)
}
