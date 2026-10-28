package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

const (
	// Un geste Proxmox finit en secondes, un instantané d'un gros disque en
	// minutes. Au-delà, l'agent rend la main : la tâche peut courir encore sur
	// l'hôte, il ne sait pas l'annuler et ne force rien.
	proxmoxTaskTimeout = 10 * time.Minute
	proxmoxPollPeriod  = 2 * time.Second
	// L'état d'un invité tel que Proxmox le dit.
	statusRunning = "running"
	statusStopped = "stopped"
)

// proxmoxTools est ce que les ordres proxmox.vm.* touchent : l'état de l'agent,
// où est le jeton, et l'API locale. Les tests changent l'adresse (un faux
// Proxmox) et la cadence.
type proxmoxTools struct {
	stateDir   *os.Root
	apiURL     string
	pollPeriod time.Duration
	timeout    time.Duration
	now        func() time.Time
}

func newProxmoxTools(stateDir *os.Root) proxmoxTools {
	return proxmoxTools{stateDir: stateDir, apiURL: machine.ProxmoxLocalAPI, pollPeriod: proxmoxPollPeriod, timeout: proxmoxTaskTimeout, now: time.Now}
}

// proxmoxJob est un ordre jugé : ce que l'exécution lira de lui.
type proxmoxJob struct {
	kind   agentwire.Kind
	params agentwire.ProxmoxGuestParams
	token  proxmoxToken
}

// readProxmoxOrder juge l'ordre avant de l'accepter : des paramètres stricts
// (un champ inconnu ne passe pas), un instantané de ce produit pour la
// suppression, et un jeton local utilisable. Un refus ici n'a rien appelé.
func (t proxmoxTools) readProxmoxOrder(body agentwire.OrderBody) (proxmoxJob, *agentwire.ActionResult) {
	var params agentwire.ProxmoxGuestParams
	decoder := json.NewDecoder(bytes.NewReader(body.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return proxmoxJob{}, &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	if err := params.Validate(body.Kind); err != nil {
		reason := agentwire.ReasonBadOrder
		if agentwire.IsSnapshotNotOurs(err) {
			reason = agentwire.ReasonProxmoxSnapshotNotOurs
		}
		return proxmoxJob{}, &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: reason}
	}
	token, reason := readProxmoxToken(t.stateDir)
	if reason != "" {
		return proxmoxJob{}, &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: reason}
	}
	return proxmoxJob{kind: body.Kind, params: params, token: token}, nil
}

// run fait le geste : retrouver l'invité, vérifier qu'il peut le recevoir,
// appeler l'API, suivre la tâche jusqu'à sa fin. Tout ce qui est refusé avant
// l'appel qui change quelque chose est « untouched » ; une tâche qui échoue ou
// qui traîne est un échec réel.
func (t proxmoxTools) run(ctx context.Context, job proxmoxJob) ([]agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	api, err := newProxmoxAPI(t.apiURL, job.token)
	if err != nil {
		return nil, untouchedError(err)
	}
	guest, err := findGuest(ctx, api, job.params)
	if err != nil {
		return nil, err
	}
	if err := checkGuestState(ctx, api, guest, job.kind); err != nil {
		return nil, err
	}
	if job.kind == agentwire.KindProxmoxSnapshotDelete {
		if err := checkSnapshotExists(ctx, api, guest, job.params.Snapshot); err != nil {
			return nil, err
		}
	}
	return t.perform(ctx, api, guest, job)
}

// findGuest retrouve l'invité par son numéro : il doit exister, être de la
// sorte demandée, et ne pas être un modèle.
func findGuest(ctx context.Context, api *proxmoxAPI, params agentwire.ProxmoxGuestParams) (pveGuest, error) {
	guests, err := api.listGuests(ctx)
	if err != nil {
		return pveGuest{}, untouchedError(err)
	}
	index := slices.IndexFunc(guests, func(guest pveGuest) bool { return guest.VMID == params.VMID })
	if index < 0 || guests[index].Type != params.Type {
		return pveGuest{}, refuseWith(agentwire.ReasonProxmoxGuestMissing, fmt.Errorf("no %s guest %d", params.Type, params.VMID))
	}
	guest := guests[index]
	if guest.Template == 1 {
		return pveGuest{}, refuseWith(agentwire.ReasonProxmoxTemplate, fmt.Errorf("guest %d is a template", params.VMID))
	}
	if !nodeNamePattern.MatchString(guest.Node) {
		return pveGuest{}, refuseWith(agentwire.ReasonProxmoxBadResponse, errors.New("the node name is not a host name"))
	}
	return guest, nil
}

// checkGuestState relit l'état de l'invité : démarrer demande qu'il soit
// arrêté, l'arrêter qu'il soit en marche. Un instantané se prend dans les deux
// états.
func checkGuestState(ctx context.Context, api *proxmoxAPI, guest pveGuest, kind agentwire.Kind) error {
	wanted := ""
	switch kind {
	case agentwire.KindProxmoxStart:
		wanted = statusStopped
	case agentwire.KindProxmoxShutdown:
		wanted = statusRunning
	default:
		return nil
	}
	status, err := api.guestStatus(ctx, guest)
	if err != nil {
		return untouchedError(err)
	}
	if status != wanted {
		return refuseWith(agentwire.ReasonProxmoxStateMismatch, fmt.Errorf("guest %d is %q, not %q", guest.VMID, status, wanted))
	}
	return nil
}

// checkSnapshotExists : on ne supprime que ce qu'on voit, jamais à l'aveugle.
func checkSnapshotExists(ctx context.Context, api *proxmoxAPI, guest pveGuest, name string) error {
	names, err := api.listSnapshots(ctx, guest)
	if err != nil {
		return untouchedError(err)
	}
	if !slices.Contains(names, name) {
		return refuseWith(agentwire.ReasonProxmoxSnapshotMissing, fmt.Errorf("guest %d has no snapshot %q", guest.VMID, name))
	}
	return nil
}

// perform appelle l'API pour le genre de l'ordre et suit la tâche. Jamais
// d'arrêt forcé (forceStop), d'état de mémoire (vmstate), de description, de
// retour en arrière ni de suppression d'invité : ces appels n'existent pas ici.
func (t proxmoxTools) perform(ctx context.Context, api *proxmoxAPI, guest pveGuest, job proxmoxJob) ([]agentwire.OutputLine, error) {
	target := fmt.Sprintf("%s/%d", guest.Type, guest.VMID)
	var upid, done string
	var err error
	switch job.kind {
	case agentwire.KindProxmoxStart:
		upid, err = api.startTask(ctx, http.MethodPost, guestPath(guest)+"/status/start", nil)
		done = "started " + target
	case agentwire.KindProxmoxShutdown:
		upid, err = api.startTask(ctx, http.MethodPost, guestPath(guest)+"/status/shutdown", nil)
		done = "shutdown " + target
	case agentwire.KindProxmoxSnapshot:
		name := agentwire.SnapshotName(t.now())
		upid, err = api.startTask(ctx, http.MethodPost, guestPath(guest)+"/snapshot", url.Values{"snapname": {name}})
		done = "snapshot " + target + " " + name
	case agentwire.KindProxmoxSnapshotDelete:
		upid, err = api.startTask(ctx, http.MethodDelete, guestPath(guest)+"/snapshot/"+url.PathEscape(job.params.Snapshot), nil)
		done = "snapshot_deleted " + target + " " + job.params.Snapshot
	default:
		return nil, refuseWith(agentwire.ReasonUnknownKind, errors.New("not a guest order"))
	}
	if err != nil {
		// Proxmox a refusé l'appel avant de créer la tâche : rien n'a changé.
		return nil, untouchedError(err)
	}
	output := []agentwire.OutputLine{stdout("task " + upid)}
	if err := t.followTask(ctx, api, guest.Node, upid); err != nil {
		return append(output, agentwire.OutputLine{Stream: "stderr", Line: cleanLine(err.Error())}), err
	}
	return append(output, stdout(done)), nil
}

// followTask attend la fin de la tâche de Proxmox et rend son issue réelle.
// « OK » et « WARNINGS… » sont des réussites ; le reste, un échec avec le
// code de sortie de Proxmox.
func (t proxmoxTools) followTask(ctx context.Context, api *proxmoxAPI, node, upid string) error {
	for {
		running, exitStatus, err := api.taskState(ctx, node, upid)
		switch {
		case err != nil && ctx.Err() != nil:
			return timedOut()
		case err != nil:
			return failWith(proxmoxReasonOf(err), err)
		case !running:
			return judgeExitStatus(exitStatus)
		}
		if !wait.For(ctx, t.pollPeriod) {
			return timedOut()
		}
	}
}

func judgeExitStatus(exitStatus string) error {
	if exitStatus == "OK" || strings.HasPrefix(exitStatus, "WARNINGS") {
		return nil
	}
	return failWith(agentwire.ReasonProxmoxTaskFailed, fmt.Errorf("the Proxmox task ended with %q", cutText(exitStatus, maxProxmoxMessageBytes)))
}

func timedOut() error {
	return failWith(agentwire.ReasonProxmoxTaskTimeout, errors.New("the Proxmox task did not finish in time; it may still run on the host"))
}

// untouchedError donne à un échec de l'API la raison qu'elle porte, comme refus
// d'avant l'écriture.
func untouchedError(err error) error {
	return refuseWith(proxmoxReasonOf(err), err)
}

func proxmoxReasonOf(err error) string {
	var failure *pveError
	if errors.As(err, &failure) {
		return failure.reason
	}
	return agentwire.ReasonProxmoxBadResponse
}
