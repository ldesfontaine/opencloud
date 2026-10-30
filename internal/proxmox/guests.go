package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Verb est un geste sur un invité Proxmox que l'opérateur demande. La liste est
// fermée : démarrer, arrêter proprement, prendre un instantané, supprimer un
// instantané créé par ce produit. Ni arrêt forcé, ni redémarrage, ni retour en
// arrière, ni suppression d'invité.
type Verb string

const (
	VerbStart          Verb = "start"
	VerbShutdown       Verb = "shutdown"
	VerbSnapshot       Verb = "snapshot"
	VerbSnapshotDelete Verb = "snapshot-delete"
)

func (v Verb) kind() agentwire.Kind {
	switch v {
	case VerbStart:
		return agentwire.KindProxmoxStart
	case VerbShutdown:
		return agentwire.KindProxmoxShutdown
	case VerbSnapshot:
		return agentwire.KindProxmoxSnapshot
	}
	return agentwire.KindProxmoxSnapshotDelete
}

var (
	// Le serveur refuse avant d'envoyer ce qu'il sait que l'agent refuserait,
	// et ce qu'il ne doit jamais demander.
	ErrGuestUnknown = refusal.Conflict("proxmox.guest_unknown", "the guest is not in the last reading of the host")
	ErrGuestInvalid = refusal.Invalid("proxmox.guest_invalid", "the guest number must be a Proxmox VM number")
	ErrTemplate     = refusal.Conflict("proxmox.guest_template", "a template takes no power or snapshot order")
	ErrStateWrong   = refusal.Conflict("proxmox.state_mismatch", "the guest state does not allow this order: start needs a stopped guest, shutdown a running one")
	ErrSelfTarget   = refusal.Conflict("proxmox.self_target", "the guest hosts this server: it is never shut down from here")
	ErrSnapshotName = refusal.Invalid("proxmox.snapshot_invalid", "only a snapshot this product created can be deleted")
	// ErrNotWired : le composant n'a pas reçu ses ordres (une commande hors
	// ligne).
	ErrNotWired = errors.New("proxmox guest orders not wired")
)

// Orders est ce que le composant attend du composant action : envoyer un ordre
// signé à l'agent de l'hôte, et attendre sa fin.
type Orders interface {
	Send(ctx context.Context, machineID string, kind agentwire.Kind, params json.RawMessage) (action.Run, error)
	Wait(ctx context.Context, runID string) (action.Run, error)
}

// Viewer lit les invités du dernier relevé d'un hôte (le store).
type Viewer interface {
	GetProxmoxView(ctx context.Context, machineID string) (View, error)
}

// followLimit borne l'attente de la fin d'un ordre : le serveur le dit fini de
// lui-même, expiré ou échoué, mais une boucle de fond arrêtée ne retient pas
// ce suivi pour toujours.
const followLimit = action.OrderLifetime + action.MaxRunDuration + 5*time.Minute

func (s *Service) SetOrders(orders Orders)         { s.orders = orders }
func (s *Service) SetViewer(viewer Viewer)         { s.viewer = viewer }
func (s *Service) SetLifetime(ctx context.Context) { s.lifetime = ctx }

// Act envoie à l'agent de l'hôte l'ordre qui fait le geste sur l'invité. Le
// serveur juge d'abord, sur le dernier relevé : l'invité existe, n'est pas un
// modèle, son état permet le geste, et ce n'est jamais l'arrêt de la machine
// qui porte ce serveur. L'agent revérifie tout par l'API de Proxmox, qui a le
// dernier mot ; le relevé de l'hôte est relu quand l'ordre réussit.
func (s *Service) Act(ctx context.Context, machineID string, vmid int, verb Verb, snapshot string) (action.Run, error) {
	if s.orders == nil || s.viewer == nil {
		return action.Run{}, ErrNotWired
	}
	guest, err := s.guestFor(ctx, machineID, vmid)
	if err != nil {
		return action.Run{}, err
	}
	params := agentwire.ProxmoxGuestParams{VMID: guest.VMID, Type: guest.Kind, Snapshot: snapshot}
	if err := judgeGesture(guest, verb, params); err != nil {
		return action.Run{}, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return action.Run{}, fmt.Errorf("encode guest params: %w", err)
	}
	run, err := s.orders.Send(ctx, machineID, verb.kind(), encoded)
	if err != nil {
		return action.Run{}, err
	}
	s.auditor.Record(ctx, guestEvent(machineID, guest, verb, run))
	s.refreshAfter(ctx, machineID, run)
	return run, nil
}

// guestFor retrouve l'invité dans le dernier relevé ; l'hôte non déclaré est
// ErrNotFound.
func (s *Service) guestFor(ctx context.Context, machineID string, vmid int) (Guest, error) {
	view, err := s.viewer.GetProxmoxView(ctx, machineID)
	if err != nil {
		return Guest{}, err
	}
	index := slices.IndexFunc(view.Guests, func(guest Guest) bool { return guest.VMID == vmid })
	if index < 0 {
		return Guest{}, ErrGuestUnknown
	}
	return view.Guests[index], nil
}

// judgeGesture refuse ce que le serveur sait impossible ou interdit. L'état
// vient du dernier relevé, vieux de cinq minutes au plus : l'agent le relit.
func judgeGesture(guest Guest, verb Verb, params agentwire.ProxmoxGuestParams) error {
	if guest.Template {
		return ErrTemplate
	}
	if err := params.Validate(verb.kind()); err != nil {
		if agentwire.IsSnapshotNotOurs(err) {
			return ErrSnapshotName
		}
		return ErrGuestInvalid
	}
	switch {
	case verb == VerbStart && guest.Status != "stopped":
		return ErrStateWrong
	case verb == VerbShutdown && guest.Status != "running":
		return ErrStateWrong
	case verb == VerbShutdown && guest.LinkedMachineID == machine.LocalID:
		return ErrSelfTarget
	}
	return nil
}

// refreshAfter relit l'hôte quand l'ordre a réussi : l'état de l'invité a
// changé. Le suivi vit avec le serveur, pas avec la requête, et garde l'acteur
// qui a demandé pour l'audit.
func (s *Service) refreshAfter(ctx context.Context, machineID string, run action.Run) {
	followCtx := audit.WithActor(s.lifetime, audit.ActorFrom(ctx))
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		waitCtx, cancel := context.WithTimeout(followCtx, followLimit)
		defer cancel()
		finished, err := s.orders.Wait(waitCtx, run.ID)
		if err != nil || finished.State != action.StateSucceeded {
			return
		}
		if err := s.Refresh(followCtx, machineID); err != nil && !errors.Is(err, ErrNotFound) {
			s.logRefresh(machineID, err)
		}
	}()
}

// WaitBackground attend les suivis en cours : l'arrêt du serveur, ou un test.
func (s *Service) WaitBackground() { s.background.Wait() }

// guestEvent dit quel geste est demandé sur quel invité, avec le run : la
// ligne action.order dit l'ordre, celle-ci dit pour quelle VM.
func guestEvent(machineID string, guest Guest, verb Verb, run action.Run) audit.Event {
	details := audit.Details{
		"verb": string(verb), "vmid": fmt.Sprint(guest.VMID), "type": guest.Kind, "run_id": run.ID,
	}
	return audit.Event{Action: audit.MachineProxmoxGuest, ObjectID: machineID, ObjectName: guestLabel(guest), Details: details}
}

// guestLabel nomme l'invité dans l'audit : son nom et son numéro.
func guestLabel(guest Guest) string {
	return strings.TrimSpace(fmt.Sprintf("%s (%d)", guest.Name, guest.VMID))
}

// OwnSnapshots rend les noms des instantanés de ce produit que les ordres
// réussis ont créés sur l'invité et que nul ordre réussi n'a supprimés depuis.
// Le relevé ne liste pas les instantanés (le jeton du serveur ne fait que lire
// l'hôte) : les runs sont la seule trace, et l'agent revérifie avant de
// supprimer.
func OwnSnapshots(runs []action.Run, vmid int) []string {
	// Du plus ancien au plus récent : une suppression défait la création d'avant.
	snapshots := []string{}
	for _, run := range slices.Backward(runs) {
		if run.State != action.StateSucceeded {
			continue
		}
		name, ok := snapshotInRun(run, vmid)
		if !ok {
			continue
		}
		if run.Kind == agentwire.KindProxmoxSnapshot {
			snapshots = append(snapshots, name)
		} else {
			snapshots = slices.DeleteFunc(snapshots, func(known string) bool { return known == name })
		}
	}
	return snapshots
}

// snapshotInRun lit le nom de l'instantané qu'un run de ce genre a créé ou
// supprimé sur cet invité : la ligne de sortie « snapshot qemu/105 oc-… ».
func snapshotInRun(run action.Run, vmid int) (string, bool) {
	if run.Kind != agentwire.KindProxmoxSnapshot && run.Kind != agentwire.KindProxmoxSnapshotDelete {
		return "", false
	}
	var params agentwire.ProxmoxGuestParams
	if json.Unmarshal(run.Params, &params) != nil || params.VMID != vmid {
		return "", false
	}
	for _, line := range run.Output {
		fields := strings.Fields(line.Line)
		if len(fields) == 3 && (fields[0] == "snapshot" || fields[0] == "snapshot_deleted") && agentwire.IsOwnSnapshot(fields[2]) {
			return fields[2], true
		}
	}
	return "", false
}
