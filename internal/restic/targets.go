package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// TargetSetup est ce que l'opérateur demande pour faire d'une machine une
// cible : l'adresse que les sources joindront, le port de rest-server, et le
// dossier des dépôts, celui du droit restic.target. Floor relève le
// plancher de rétention de la cible ; nil, elle garde le sien.
type TargetSetup struct {
	Host  string
	Port  int
	Dir   string
	Floor *agentwire.ResticRetentionFloor
}

// TargetRecord est une cible : ce qui a été demandé, et ce que son agent
// rapporte (vide tant qu'il n'a rien dit).
type TargetRecord struct {
	MachineID   string
	MachineName string
	Requested   TargetSetup
	RequestedAt time.Time
	SetupRunID  string
	Reported    agentwire.ResticTargetReport
	// Zéro tant que l'agent n'a rien rapporté.
	ReportedAt time.Time
}

// IsReady dit si une source peut y être liée : l'agent a rapporté un
// rest-server en marche, son adresse et son certificat.
func (t TargetRecord) IsReady() bool {
	reported := t.Reported
	return reported.State == agentwire.ResticTargetRunning && reported.Host != "" && reported.Port != 0 && reported.Certificate != ""
}

var (
	ErrTargetUnknown = refusal.Invalid("restic.target_unknown", "this machine is not a restic target")
	ErrHostInvalid   = refusal.Invalid("restic.host_invalid", "the announced address must be an IP address or a host name, without port")
	ErrPortInvalid   = refusal.Invalid("restic.port_invalid", "the port must be between 1 and 65535")
	// Le droit manque ou ne peut s'exercer : l'écran propose de ré-enrôler
	// la machine de la requête avec restic.target.
	ErrTargetRightMissing     = refusal.Conflict("restic.target_right_missing", "the agent of this machine may not run rest-server: re-enroll it with restic.target").NeedsRight(machine.CapabilityResticTarget)
	ErrTargetRightUnavailable = refusal.Conflict("restic.target_right_unavailable", "the agent holds restic.target but cannot exercise it on this machine")
	ErrTargetDirOutsideRight  = refusal.Conflict("restic.target_dir_outside_right", "the folder of the repositories must be the one of the restic.target right")
	ErrTargetInUse            = refusal.Conflict("restic.target_in_use", "backups are sent to this target: delete them first")
	// La cible refuse d'abaisser son plancher : le dire avant l'ordre.
	ErrFloorLowered = refusal.Conflict("restic.floor_lowered", "the retention floor of a target can only be raised: lowering it is done on the target machine")
	// rest-server manque sur la cible : l'écran dit quoi installer.
	ErrRestServerMissing = refusal.Conflict("restic.rest_server_missing", "rest-server is not installed on the target: install it, then restart its agent")
	// ErrMachineOffline : alias de machine.ErrOffline, le temps que les appelants passent au nom neuf.
	ErrMachineOffline = machine.ErrOffline
)

// ErrTargetNotFound : la base n'a pas cette cible (le store le rend).
var ErrTargetNotFound = errors.New("restic target not found")

// SetupTarget fait d'une machine une cible : il juge le droit restic.target
// et son dossier, envoie restic.target.setup, puis note la demande. L'état
// de rest-server arrive par le signal de l'agent.
func (s *Service) SetupTarget(ctx context.Context, machineID string, setup TargetSetup) (TargetRecord, error) {
	target, err := s.machines.Get(ctx, machineID)
	if errors.Is(err, machine.ErrNotFound) {
		return TargetRecord{}, machine.ErrUnknown
	}
	if err != nil {
		return TargetRecord{}, fmt.Errorf("get target machine: %w", err)
	}
	setup, err = judgeTargetSetup(target, setup)
	if err != nil {
		return TargetRecord{}, err
	}
	if err := s.judgeFloor(ctx, machineID, setup.Floor); err != nil {
		return TargetRecord{}, err
	}
	params, err := json.Marshal(agentwire.ResticTargetSetupParams{
		Listen: listenFor(setup.Host), Port: setup.Port, Hosts: []string{setup.Host}, RetentionFloor: setup.Floor,
	})
	if err != nil {
		return TargetRecord{}, fmt.Errorf("encode target setup: %w", err)
	}
	run, err := s.send(ctx, machineID, agentwire.KindResticTargetSetup, params, ErrTargetRightMissing)
	if err != nil {
		return TargetRecord{}, err
	}
	record := TargetRecord{MachineID: machineID, MachineName: target.Name, Requested: setup, RequestedAt: s.now(), SetupRunID: run.ID}
	if err := s.store.SaveResticTargetRequest(ctx, record); err != nil {
		return TargetRecord{}, fmt.Errorf("save restic target: %w", err)
	}
	s.logger.Info("restic target set up", "machine_id", machineID, "run_id", run.ID, "port", setup.Port)
	details := audit.Details{"host": setup.Host, "port": strconv.Itoa(setup.Port), "dir": setup.Dir, "run_id": run.ID}
	if floor := setup.Floor; floor != nil {
		details["retention_floor"] = fmt.Sprintf("%d/%d/%d", floor.KeepDaily, floor.KeepWeekly, floor.KeepMonthly)
	}
	s.auditor.Record(ctx, audit.Event{Action: audit.ResticTargetSetup, ObjectID: machineID, ObjectName: target.Name, Details: details})
	s.changed()
	return s.store.GetResticTarget(ctx, machineID)
}

// judgeTargetSetup : l'adresse, le port, puis le droit et son dossier. Un
// dossier vide prend celui du droit.
func judgeTargetSetup(target machine.Status, setup TargetSetup) (TargetSetup, error) {
	probe := agentwire.ResticTargetSetupParams{Listen: listenFor(setup.Host), Port: 1, Hosts: []string{setup.Host}}
	if probe.Validate() != nil {
		return TargetSetup{}, ErrHostInvalid
	}
	if setup.Port < 1 || setup.Port > 65535 {
		return TargetSetup{}, ErrPortInvalid
	}
	right := target.Agent.Fit(machine.CapabilityResticTarget, "")
	if err := judgeTargetRight(right); err != nil {
		return TargetSetup{}, err
	}
	if setup.Dir == "" {
		setup.Dir = right.Path
	}
	// Tant que l'agent n'a rien rapporté, il jugera lui-même son dossier.
	if right.State == machine.RightOK && setup.Dir != right.Path {
		return TargetSetup{}, ErrTargetDirOutsideRight
	}
	return setup, nil
}

// judgeFloor : un plancher dans ses bornes, jamais plus bas que celui que la
// cible a rapporté (elle le refuserait). La cible reste juge.
func (s *Service) judgeFloor(ctx context.Context, machineID string, floor *agentwire.ResticRetentionFloor) error {
	if floor == nil {
		return nil
	}
	if floor.Validate() != nil {
		return ErrRetentionInvalid
	}
	current, err := s.store.GetResticTarget(ctx, machineID)
	if errors.Is(err, ErrTargetNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get restic target: %w", err)
	}
	if known := current.Reported.RetentionFloor; known != nil && floor.IsLowerThan(*known) {
		return ErrFloorLowered
	}
	return nil
}

// judgeTargetRight : le droit restic.target, et rest-server présent.
func judgeTargetRight(right machine.RightFit) error {
	switch {
	case right.State == machine.RightMissing:
		return ErrTargetRightMissing
	case right.State == machine.RightUnavailable && right.Reason == agentwire.ReasonRestServerMissing:
		return ErrRestServerMissing
	case right.State == machine.RightUnavailable:
		return ErrTargetRightUnavailable
	}
	return nil
}

// listenFor : rest-server écoute sur toutes les adresses de la famille de
// l'adresse annoncée ; un nom DNS se résout plus tard, IPv4 par défaut.
func listenFor(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "::"
	}
	return "0.0.0.0"
}

// RemoveTarget retire une cible qu'aucune sauvegarde n'utilise. rest-server
// reste installé et ses dépôts restent sur le disque : rien ne s'efface.
func (s *Service) RemoveTarget(ctx context.Context, machineID string) error {
	target, err := s.store.GetResticTarget(ctx, machineID)
	if errors.Is(err, ErrTargetNotFound) {
		return ErrTargetUnknown
	}
	if err != nil {
		return fmt.Errorf("get restic target: %w", err)
	}
	backups, err := s.store.ListResticBackups(ctx)
	if err != nil {
		return fmt.Errorf("list restic backups: %w", err)
	}
	for _, existing := range backups {
		if existing.TargetMachineID == machineID {
			return ErrTargetInUse
		}
	}
	if err := s.store.DeleteResticTarget(ctx, machineID); err != nil {
		return fmt.Errorf("delete restic target: %w", err)
	}
	s.logger.Info("restic target removed", "machine_id", machineID)
	s.auditor.Record(ctx, audit.Event{Action: audit.ResticTargetRemove, ObjectID: machineID, ObjectName: target.MachineName})
	s.changed()
	return nil
}

// send envoie un ordre et traduit ses refus : le droit manquant en celui du
// geste (rightRefusal), la machine hors ligne en celui du composant.
func (s *Service) send(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage, rightRefusal error) (action.Run, error) {
	run, err := s.sender.Send(ctx, machineID, kind, params)
	return translateSend(run, err, kind, rightRefusal)
}

// sendSecret fait comme send avec un secret signé dans l'ordre. Le secret
// part à l'agent de la machine et à lui seul : ni le run, ni l'audit, ni un
// journal ne le gardent (action.SendWithSecret).
func (s *Service) sendSecret(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage, secret any, rightRefusal error) (action.Run, error) {
	encoded, err := json.Marshal(secret)
	if err != nil {
		return action.Run{}, fmt.Errorf("encode %s secret", kind)
	}
	run, err := s.sender.SendWithSecret(ctx, machineID, kind, params, encoded, secretLabel)
	return translateSend(run, err, kind, rightRefusal)
}

func translateSend(run action.Run, err error, kind agentwire.Kind, rightRefusal error) (action.Run, error) {
	switch {
	case errors.Is(err, action.ErrRightMissing), errors.Is(err, machine.ErrNoAgent):
		return action.Run{}, rightRefusal
	case errors.Is(err, action.ErrMachineOffline):
		return action.Run{}, ErrMachineOffline
	case err != nil:
		return action.Run{}, fmt.Errorf("send %s: %w", kind, err)
	}
	// Écrit mais pas parti (flux tombé entre-temps) : hors ligne.
	if run.State.IsFinished() {
		return action.Run{}, ErrMachineOffline
	}
	return run, nil
}
