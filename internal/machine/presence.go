package machine

import "time"

// openStreams est la présence de serve : l'agent tient un flux ouvert.
type openStreams struct {
	sessions *Sessions
}

func (p openStreams) IsOnline(machine Machine, _ time.Time) bool {
	return p.sessions.IsOnline(machine.ID)
}

// ObservedSince est le plus ancien dernier signal qui compte encore comme
// frais à cet instant : le seuil de Observed, pour qui le juge en SQL.
func ObservedSince(now time.Time) time.Time {
	return now.Add(-LostAfter)
}

// Observed dit si le serveur a des nouvelles fraîches de la machine : son
// agent est actif et son dernier signal date de LostAfter au plus. C'est le
// dernier signal (envoyé toutes les 30 s) qui juge, pas le flux ouvert : une
// machine coupée à chaud garde son flux ouvert jusqu'au délai de la
// connexion, alors que les signaux, eux, cessent tout de suite. Le seuil
// laisse passer le redémarrage d'un agent ou d'openCloud sans que rien
// n'ait l'air perdu. Les services de la machine, la page de statut et
// l'alerte « machine perdue » appellent cette seule règle.
func Observed(status Status, now time.Time) bool {
	return status.Agent.IsActive() && !status.LastSeenAt.Before(ObservedSince(now))
}
