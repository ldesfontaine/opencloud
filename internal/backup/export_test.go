package backup

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Due ouvre la règle du rythme aux tests de backup_test, qui ne peuvent pas
// être internes (le banc de test importe le store, qui importe ce paquet).
func Due(now time.Time, plan Plan) bool {
	return due(now, plan)
}

// Settle attend la fin des passages menés en arrière-plan par RunNow.
func (s *Service) Settle() {
	s.background.Wait()
}

// CheckDestinationVersion ouvre le garde-fou sur l'agent de la destination à
// une version écrite en clair.
func CheckDestinationVersion(version string) error {
	return checkDestinationAgent(machine.Status{Machine: machine.Machine{Agent: machine.Agent{Version: version}}})
}
