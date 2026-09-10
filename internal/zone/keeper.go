package zone

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/cloudflare"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

// Store est ce que zone écrit et relit. Le vrai est *store.Store.
type Store interface {
	Zones(ctx context.Context) ([]store.Zone, error)
	Zone(ctx context.Context, name string) (store.Zone, error)
	InsertZone(ctx context.Context, zone store.Zone) error
	MarkZoneRotated(ctx context.Context, name, cloudflareID string, at time.Time) error
	DeleteZone(ctx context.Context, name string) error
	ZoneMachines(ctx context.Context, zone string) ([]store.ZoneMachine, error)
	RecordZoneMachine(ctx context.Context, placement store.ZoneMachine) error
	Domains(ctx context.Context) ([]store.Domain, error)
	Lines(ctx context.Context, actionID string, afterSeq int64) ([]store.ActionLine, error)
}

// Cloudflare est ce que zone demande à l'API : le jeton vaut-il quelque chose,
// et la zone existe-t-elle sur son compte. Rien d'autre.
type Cloudflare interface {
	VerifyToken(ctx context.Context, token string) error
	ZoneByName(ctx context.Context, token, name string) (cloudflare.Zone, error)
}

// Zone : ce qu'openCloud sait d'une zone, jeton exclu. Fingerprint est
// l'empreinte du jeton courant — celle que doivent porter les machines.
type Zone struct {
	Name         string
	CloudflareID string
	AddedAt      time.Time
	RotatedAt    time.Time
	Fingerprint  string
	Machines     []Placement
}

// Placement : une machine qui porte le jeton d'une zone, et lequel.
type Placement struct {
	MachineID   string
	PlacedAt    time.Time
	Fingerprint string
	// Current : l'empreinte posée est celle du jeton courant. Faux après une
	// rotation, tant que l'action n'a pas été rejouée sur cette machine.
	Current bool
}

// Keeper tient les zones et leurs jetons.
type Keeper struct {
	root   *os.Root
	store  Store
	api    Cloudflare
	logger *slog.Logger
}

func New(root *os.Root, database Store, api Cloudflare, logger *slog.Logger) *Keeper {
	return &Keeper{root: root, store: database, api: api, logger: logger}
}

// Add enregistre une zone et pose son jeton. Rien n'est écrit avant que
// Cloudflare ait dit que le jeton vaut et que la zone existe.
func (k *Keeper) Add(ctx context.Context, name, token string) error {
	name, token, err := k.checkedZoneAndToken(name, token)
	if err != nil {
		return err
	}

	if _, err := k.store.Zone(ctx, name); err == nil {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("la zone « %s » est déjà enregistrée", name),
			Remedy: "faire tourner son jeton plutôt que l'ajouter une seconde fois",
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	found, err := k.verifyOnCloudflare(ctx, name, token)
	if err != nil {
		return err
	}

	if err := k.writeToken(name, token); err != nil {
		return err
	}
	added := store.Zone{Name: name, CloudflareID: found.ID, AddedAt: time.Now().UTC()}
	if err := k.store.InsertZone(ctx, added); err != nil {
		// La ligne n'est pas passée : le fichier ne doit pas rester seul.
		if removeErr := k.removeToken(name); removeErr != nil {
			k.logger.Error("remove orphan zone token", "zone", name, "error", removeErr)
		}
		return err
	}

	k.logger.Info("zone added", "zone", name, "cloudflare_id", found.ID)
	return nil
}

// Rotate remplace le jeton d'une zone. Même vérification qu'à l'ajout : un
// jeton qui ne vaut rien ne remplace pas un jeton qui vaut.
func (k *Keeper) Rotate(ctx context.Context, name, token string) error {
	name, token, err := k.checkedZoneAndToken(name, token)
	if err != nil {
		return err
	}
	if err := k.requireZone(ctx, name); err != nil {
		return err
	}

	found, err := k.verifyOnCloudflare(ctx, name, token)
	if err != nil {
		return err
	}

	if err := k.writeToken(name, token); err != nil {
		return err
	}
	if err := k.store.MarkZoneRotated(ctx, name, found.ID, time.Now().UTC()); err != nil {
		return err
	}

	k.logger.Info("zone token rotated", "zone", name)
	return nil
}

// Remove retire une zone, son jeton et ses machines. Un domaine publié sous la
// zone la retient : c'est un refus, pas une suppression en cascade.
func (k *Keeper) Remove(ctx context.Context, name string) error {
	if err := k.requireZone(ctx, name); err != nil {
		return err
	}

	published, err := k.store.Domains(ctx)
	if err != nil {
		return err
	}
	if held := namesUnderZone(published, name); len(held) > 0 {
		return refusal.Refusal{
			Cause: fmt.Sprintf("%d nom(s) publié(s) dépendent de la zone « %s » : %s",
				len(held), name, strings.Join(held, ", ")),
			Remedy: "retirer ces hôtes virtuels, chacun par son action, puis retirer la zone",
		}
	}

	if err := k.removeToken(name); err != nil {
		return err
	}
	if err := k.store.DeleteZone(ctx, name); err != nil {
		return err
	}

	k.logger.Info("zone removed", "zone", name)
	return nil
}

// Zones rend les zones enregistrées, avec l'empreinte du jeton courant et les
// machines qui le portent.
func (k *Keeper) Zones(ctx context.Context) ([]Zone, error) {
	registered, err := k.store.Zones(ctx)
	if err != nil {
		return nil, err
	}

	var zones []Zone
	for _, entry := range registered {
		current, err := k.currentFingerprint(entry.Name)
		if err != nil {
			return nil, err
		}
		placements, err := k.placements(ctx, entry.Name, current)
		if err != nil {
			return nil, err
		}
		zones = append(zones, Zone{
			Name:         entry.Name,
			CloudflareID: entry.CloudflareID,
			AddedAt:      entry.AddedAt,
			RotatedAt:    entry.RotatedAt,
			Fingerprint:  current,
			Machines:     placements,
		})
	}
	return zones, nil
}

// Names rend les noms des zones enregistrées : de quoi remplir la liste du
// formulaire d'action, sans en dire plus.
func (k *Keeper) Names(ctx context.Context) ([]string, error) {
	registered, err := k.store.Zones(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(registered))
	for _, entry := range registered {
		names = append(names, entry.Name)
	}
	return names, nil
}

func (k *Keeper) placements(ctx context.Context, zone, current string) ([]Placement, error) {
	rows, err := k.store.ZoneMachines(ctx, zone)
	if err != nil {
		return nil, err
	}

	var placements []Placement
	for _, row := range rows {
		placements = append(placements, Placement{
			MachineID:   row.MachineID,
			PlacedAt:    row.PlacedAt,
			Fingerprint: row.Fingerprint,
			Current:     current != "" && row.Fingerprint == current,
		})
	}
	return placements, nil
}

// currentFingerprint lit le fichier de jeton et rend son empreinte. Un fichier
// disparu rend une empreinte vide : la vue le dira, elle ne plantera pas.
func (k *Keeper) currentFingerprint(zone string) (string, error) {
	content, err := k.readToken(zone)
	if errors.Is(err, os.ErrNotExist) {
		k.logger.Error("zone token file is missing", "zone", zone)
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return Fingerprint(content), nil
}

// checkedZoneAndToken valide le nom de zone et nettoie le jeton, avant tout
// appel réseau et toute écriture.
func (k *Keeper) checkedZoneAndToken(name, token string) (string, string, error) {
	name = strings.TrimSpace(name)
	if err := validate.Domain(name); err != nil {
		return "", "", refusal.Refusal{
			Cause:  fmt.Sprintf("« %s » n'est pas un nom de zone", name),
			Remedy: "saisir le nom de la zone en minuscules, comme « exemple.fr »",
		}
	}
	cleaned, err := cleanToken(token)
	if err != nil {
		return "", "", err
	}
	return name, cleaned, nil
}

func (k *Keeper) requireZone(ctx context.Context, name string) error {
	_, err := k.store.Zone(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("la zone « %s » n'est pas enregistrée", name),
			Remedy: "l'ajouter d'abord dans la section « Zones Cloudflare »",
		}
	}
	return err
}

// verifyOnCloudflare demande les deux choses qui comptent : le jeton vaut, et
// il voit cette zone. Les deux, sinon rien n'est écrit.
func (k *Keeper) verifyOnCloudflare(ctx context.Context, name, token string) (cloudflare.Zone, error) {
	if err := k.api.VerifyToken(ctx, token); err != nil {
		return cloudflare.Zone{}, refuseCloudflare(name, err)
	}
	found, err := k.api.ZoneByName(ctx, token, name)
	if err != nil {
		return cloudflare.Zone{}, refuseCloudflare(name, err)
	}
	return found, nil
}

// refuseCloudflare met en français ce que Cloudflare a refusé. Le jeton n'y
// entre jamais : ni dans la cause, ni dans le remède.
func refuseCloudflare(name string, err error) error {
	switch {
	case errors.Is(err, cloudflare.ErrInvalidToken):
		return refusal.Refusal{
			Cause:  "Cloudflare refuse ce jeton : il est invalide, révoqué ou expiré",
			Remedy: "créer un jeton d'API « Zone / DNS / Edit » sur cette zone, puis le coller ici",
		}
	case errors.Is(err, cloudflare.ErrZoneNotFound):
		return refusal.Refusal{
			Cause:  fmt.Sprintf("le jeton est valide mais ne voit aucune zone « %s »", name),
			Remedy: "vérifier le nom de la zone, et que le jeton porte bien sur elle",
		}
	case errors.Is(err, cloudflare.ErrUnreadableReply):
		return refusal.Refusal{
			Cause:  "la réponse reçue ne vient pas de l'API de Cloudflare",
			Remedy: "vérifier la sortie réseau de la machine openCloud, puis recommencer",
		}
	}
	return refusal.Refusal{
		Cause:  "Cloudflare n'a pas répondu : " + err.Error(),
		Remedy: "vérifier la sortie réseau de la machine openCloud, puis recommencer",
	}
}

// namesUnderZone : un domaine dépend de la zone dont il est un sous-nom, ou
// dont il porte exactement le nom.
func namesUnderZone(published []store.Domain, zone string) []string {
	var held []string
	for _, domain := range published {
		if domain.Name == zone || strings.HasSuffix(domain.Name, "."+zone) {
			held = append(held, domain.Name)
		}
	}
	return held
}
