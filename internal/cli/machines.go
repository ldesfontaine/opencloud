package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/ldesfontaine/opencloud/internal/enroll"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/internal/web"
)

// machineAccess est le point de câblage entre l'enrôlement et ce que runner,
// web et la sonde attendent d'une machine : son état d'enrôlement, la commande
// à coller, la confirmation par empreinte, et son transport.
type machineAccess struct {
	root *os.Root
}

// enrolment : ce que l'interface web attend du câblage pour enrôler une
// machine. Déclaré ici tant que serve.go ne le branche pas.
type enrolment interface {
	Prepare(ctx context.Context, machine store.Machine) (string, error)
	Confirm(ctx context.Context, machine store.Machine, fingerprint string) error
	UpdateAccess(ctx context.Context, machine store.Machine, fingerprint string) error
	Status(ctx context.Context, machineID string) (web.EnrolmentStatus, error)
}

var (
	_ runner.Transports = machineAccess{}
	_ enrolment         = machineAccess{}
	_ probe.Transports  = machineProbes{}
)

// For rend le transport d'une machine enrôlée, la machine openCloud comprise :
// elle passe par SSH vers localhost comme les autres (05-execution.md).
func (m machineAccess) For(ctx context.Context, machine store.Machine) (transport.Transport, error) {
	client, err := m.client(ctx, machine)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (m machineAccess) client(_ context.Context, machine store.Machine) (*transport.SSH, error) {
	status, err := enroll.ReadStatus(m.root, machine.ID)
	if err != nil {
		return nil, fmt.Errorf("lire l'enrôlement de %s : %w", machine.ID, err)
	}
	if !status.Enrolled {
		return nil, runner.ErrNotEnrolled
	}
	return transport.NewSSH(enroll.EndpointOf(m.root, machine)), nil
}

// Prepare engendre la paire de clés de la machine si elle manque et rend la
// commande à coller dessus. Elle ne touche à rien sur la machine.
func (m machineAccess) Prepare(_ context.Context, machine store.Machine) (string, error) {
	publicKey, err := enroll.PrepareRemote(m.root, machine.ID)
	if err != nil {
		return "", err
	}
	return enroll.Command(publicKey)
}

// Confirm reçoit l'empreinte lue sur la machine, écrit known_hosts si elle
// correspond, pose le lanceur et marque la machine enrôlée.
func (m machineAccess) Confirm(ctx context.Context, machine store.Machine, fingerprint string) error {
	return enroll.Confirm(ctx, enroll.SystemConfirmDeps(m.root), machine, fingerprint)
}

// UpdateAccess relève à nouveau les clés d'hôte et réécrit known_hosts :
// l'action nommée « changer adresse, port ou empreinte ».
func (m machineAccess) UpdateAccess(ctx context.Context, machine store.Machine, fingerprint string) error {
	return enroll.UpdateAccess(ctx, enroll.SystemConfirmDeps(m.root), machine, fingerprint)
}

func (m machineAccess) Status(_ context.Context, machineID string) (web.EnrolmentStatus, error) {
	status, err := enroll.ReadStatus(m.root, machineID)
	if err != nil {
		return web.EnrolmentStatus{}, fmt.Errorf("lire l'enrôlement de %s : %w", machineID, err)
	}
	return web.EnrolmentStatus{Enrolled: status.Enrolled, Since: status.Since}, nil
}

// machineProbes donne à la sonde le même client SSH, vu par les deux méthodes
// dont elle a besoin. Une machine non enrôlée n'a pas de transport, donc rien
// à sonder.
type machineProbes struct {
	access machineAccess
}

func (m machineProbes) For(ctx context.Context, machine store.Machine) (probe.Prober, error) {
	client, err := m.access.client(ctx, machine)
	if err != nil {
		return nil, err
	}
	return client, nil
}
