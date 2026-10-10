package machine

import "time"

// OnlineWindow : en lecture seule, une machine est en ligne si son dernier
// signal date de moins que cela. C'est la règle qui la déclare perdue, et le
// signal arrive toutes les 30 s : quatre manqués avant de basculer.
const OnlineWindow = LostAfter

// openStreams est la présence de serve : l'agent tient un flux ouvert.
type openStreams struct {
	sessions *Sessions
}

func (p openStreams) IsOnline(machine Machine, _ time.Time) bool {
	return p.sessions.IsOnline(machine.ID)
}

// RecentSignal est la présence de opencloud mcp : ce processus ne tient pas
// les flux des agents, il ne voit que le dernier signal écrit en base par serve.
type RecentSignal struct{}

func (RecentSignal) IsOnline(machine Machine, now time.Time) bool {
	return now.Sub(machine.LastSeenAt) < OnlineWindow
}
