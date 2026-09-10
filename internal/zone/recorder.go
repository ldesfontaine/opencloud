package zone

import (
	"context"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// La clé que le script écrit pour l'empreinte du jeton qu'il a posé :
// « info: jeton=3f2a1b0c9d8e ».
const tokenFact = "jeton"

// ActionConcluded est appelé par le runner à chaque conclusion. Seule « Poser
// le jeton DNS » l'intéresse, et seulement quand elle a abouti : « fait » et
// « inchangé » disent tous deux que la machine porte ce jeton.
func (k *Keeper) ActionConcluded(ctx context.Context, action store.Action) {
	if action.State != store.StateApplied || catalog.Kind(action.Kind) != catalog.KindDNSToken {
		return
	}

	zone := catalog.DNSTokenZoneOf(action.Params)
	fingerprint, found := k.observedFingerprint(ctx, action)
	if !found {
		k.logger.Warn("dns token placed without a fingerprint in the output", "action_id", action.ID, "zone", zone)
		return
	}

	placement := store.ZoneMachine{
		Zone:        zone,
		MachineID:   action.MachineID,
		PlacedAt:    time.Now().UTC(),
		Fingerprint: fingerprint,
	}
	if err := k.store.RecordZoneMachine(ctx, placement); err != nil {
		k.logger.Error("record zone machine", "action_id", action.ID, "zone", zone, "error", err)
		return
	}
	k.logger.Info("dns token placed", "action_id", action.ID, "zone", zone,
		"machine", action.MachineID, "fingerprint", fingerprint)
}

// observedFingerprint relit l'empreinte dans la sortie du script : c'est lui
// qui l'a calculée sur la machine, sur le fichier qu'il y a posé.
func (k *Keeper) observedFingerprint(ctx context.Context, action store.Action) (string, bool) {
	lines, err := k.store.Lines(ctx, action.ID, 0)
	if err != nil {
		k.logger.Error("read action lines", "action_id", action.ID, "error", err)
		return "", false
	}

	// La dernière valeur vue gagne : une action rejouée réécrit ses constats.
	fingerprint := ""
	for _, line := range lines {
		fact, isFact := strings.CutPrefix(strings.TrimSpace(line.Text), catalog.InfoPrefix)
		if !isFact {
			continue
		}
		key, value, hasValue := strings.Cut(strings.TrimSpace(fact), "=")
		if !hasValue || key != tokenFact || !isFingerprint(value) {
			continue
		}
		fingerprint = value
	}
	return fingerprint, fingerprint != ""
}

// isFingerprint : la forme que le script écrit, et rien d'autre — une ligne
// de sortie ne décide pas de ce qui entre en base.
func isFingerprint(value string) bool {
	if len(value) != FingerprintLength {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
