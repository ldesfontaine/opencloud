package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Le journal local des ordres (journald) : pour chaque ordre reçu, de quoi
// retrouver exactement ce que le serveur a signé, sans le serveur. Le run, le
// genre, le rang, le SHA-256 du corps signé, l'empreinte de la clé qui l'a
// signé, et les paramètres non secrets de la liste blanche du genre
// (agentwire.JournalParams). Jamais le secret de l'ordre, ni un champ hors de
// la liste blanche : le journal ne reçoit que ce que cette liste nomme.

// Un agent garde au plus ce nombre d'ordres en vol ; au-delà, la mémoire des
// empreintes repart de zéro plutôt que de grandir.
const maxRememberedOrders = 1024

// signedFacts est ce que le corps décodé ne dit pas de lui-même.
type signedFacts struct {
	bodySHA256 string
	// Vide tant que la signature n'est pas vérifiée.
	signingKey string
}

// orderJournal écrit les lignes des ordres. Un ordre long finit dans le
// worker, longtemps après sa réception : les empreintes attendent ici.
type orderJournal struct {
	// logger rend le journal du moment : celui de l'agent, que les tests
	// remplacent après coup.
	logger func() *slog.Logger
	mu     sync.Mutex
	facts  map[string]signedFacts
}

func newOrderJournal(logger func() *slog.Logger) *orderJournal {
	return &orderJournal{logger: logger, facts: map[string]signedFacts{}}
}

// factsOf calcule les empreintes d'un ordre reçu : le SHA-256 des octets signés
// et l'empreinte de la clé qui les a signés (vide si non vérifiée).
func factsOf(signedBody, signingKey []byte) signedFacts {
	sum := sha256.Sum256(signedBody)
	facts := signedFacts{bodySHA256: hex.EncodeToString(sum[:])}
	if len(signingKey) > 0 {
		facts.signingKey = agentwire.KeyFingerprint(signingKey)
	}
	return facts
}

// remember garde les empreintes d'un ordre jusqu'à sa dernière ligne.
func (j *orderJournal) remember(runID string, facts signedFacts) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.facts) >= maxRememberedOrders {
		clear(j.facts)
	}
	j.facts[runID] = facts
}

// forget oublie un ordre dont plus aucune ligne ne viendra.
func (j *orderJournal) forget(runID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.facts, runID)
}

// attributes compose ce que toute ligne d'ordre porte.
func (j *orderJournal) attributes(body agentwire.OrderBody) []any {
	j.mu.Lock()
	facts := j.facts[body.RunID]
	j.mu.Unlock()
	attributes := []any{"run_id", body.RunID, "kind", string(body.Kind), "seq", body.Seq}
	if facts.bodySHA256 != "" {
		attributes = append(attributes, "body_sha256", facts.bodySHA256)
	}
	if facts.signingKey != "" {
		attributes = append(attributes, "signing_key", facts.signingKey)
	}
	if params := agentwire.JournalParams(body.Kind, body.Params); len(params) > 0 {
		group := make([]any, 0, 2*len(params))
		for name, value := range params {
			group = append(group, name, value)
		}
		attributes = append(attributes, slog.Group("params", group...))
	}
	return attributes
}

// received écrit la ligne de réception d'un ordre jugé : accepté, ou refusé
// avec sa raison. Un refus est sa dernière ligne ; un ordre accepté a encore sa
// fin à dire.
func (j *orderJournal) received(body agentwire.OrderBody, reason string) {
	if reason != "" {
		j.refused(body, reason)
		return
	}
	j.logger().Info("order received", j.attributes(body)...)
}

func (j *orderJournal) refused(body agentwire.OrderBody, reason string) {
	j.final(slog.LevelWarn, "order refused", body, "reason", reason)
}

// notAccepted : l'ordre a passé le juge mais l'exécuteur de son genre le refuse.
func (j *orderJournal) notAccepted(body agentwire.OrderBody, reason string) {
	j.final(slog.LevelWarn, "order not accepted", body, "reason", reason)
}

func (j *orderJournal) executed(body agentwire.OrderBody) {
	j.final(slog.LevelInfo, "order executed", body)
}

func (j *orderJournal) failed(body agentwire.OrderBody, err error) {
	j.final(slog.LevelWarn, "order failed", body, "error", err)
}

// final écrit la dernière ligne d'un ordre (exécuté, échoué, non accepté,
// refusé) puis en oublie les empreintes. extra : des couples nom, valeur.
func (j *orderJournal) final(level slog.Level, message string, body agentwire.OrderBody, extra ...any) {
	j.logger().Log(context.Background(), level, message, append(j.attributes(body), extra...)...)
	j.forget(body.RunID)
}
