package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ldesfontaine/opencloud/internal/enroll"
	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/internal/web"
)

// machineAccess est le point de câblage entre l'enrôlement et ce que runner
// et web attendent d'une machine : son état d'enrôlement, et son transport.
// Aujourd'hui seule la machine openCloud existe, jointe par SSH vers localhost.
type machineAccess struct {
	root *os.Root
}

func (m machineAccess) For(_ context.Context, machine store.Machine) (transport.Transport, error) {
	if machine.ID != enroll.LocalMachineID {
		// L'enrôlement à distance viendra avec le ticket suivant.
		return nil, runner.ErrNotEnrolled
	}
	status, err := enroll.ReadStatus(m.root, machine.ID)
	if err != nil {
		return nil, fmt.Errorf("lire l'enrôlement de %s : %w", machine.ID, err)
	}
	if !status.Enrolled {
		return nil, runner.ErrNotEnrolled
	}
	return transport.NewSSH(enroll.LocalEndpoint(m.root)), nil
}

func (m machineAccess) Status(_ context.Context, machineID string) (web.EnrolmentStatus, error) {
	status, err := enroll.ReadStatus(m.root, machineID)
	if err != nil {
		return web.EnrolmentStatus{}, fmt.Errorf("lire l'enrôlement de %s : %w", machineID, err)
	}
	return web.EnrolmentStatus{Enrolled: status.Enrolled, Since: status.Since}, nil
}
