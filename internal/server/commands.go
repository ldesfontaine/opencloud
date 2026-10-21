package server

import (
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Command fait du serveur le commandeur des agents : c'est lui qui tient
// leurs flux. La commande typée s'encode ici, une fois, puis part par le
// flux de la machine. Une machine sans flux est hors ligne pour le relais ;
// une file pleine est un refus, pas une panne.
func (s *Server) Command(machineID string, command agentwire.Command) error {
	event, data, err := command.Encode()
	if err != nil {
		return fmt.Errorf("encode command: %w", err)
	}
	err = s.machines.Push(machineID, machine.StreamEvent{Name: event, Data: data})
	if errors.Is(err, machine.ErrNotConnected) {
		return container.ErrMachineOffline
	}
	if errors.Is(err, machine.ErrBusy) {
		return fmt.Errorf("command %s: %w", command.Kind, err)
	}
	return err
}

// Les commandeurs de chaque domaine : le domaine dit quoi, le protocole
// (agentwire) dit sous quel nom.

func (s *Server) RequestLogs(machineID string, request container.LogRequest) error {
	return s.Command(machineID, agentwire.LogsCommand(request))
}

func (s *Server) StopLogs(machineID, requestID string) error {
	return s.Command(machineID, agentwire.StopLogsCommand(requestID))
}

func (s *Server) AssignProbes(machineID string, assignment probe.Assignment) error {
	return s.Command(machineID, agentwire.ProbesCommand(assignment))
}

func (s *Server) AssignImages(machineID string, assignment update.Assignment) error {
	return s.Command(machineID, agentwire.ImagesCommand(assignment))
}
