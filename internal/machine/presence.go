package machine

import "time"

// openStreams est la présence de serve : l'agent tient un flux ouvert.
type openStreams struct {
	sessions *Sessions
}

func (p openStreams) IsOnline(machine Machine, _ time.Time) bool {
	return p.sessions.IsOnline(machine.ID)
}
