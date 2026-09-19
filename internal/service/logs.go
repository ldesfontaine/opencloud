package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// LogSource sait ouvrir un journal sur une machine : en processus pour la
// machine openCloud, par l'agent pour les autres. Le canal se ferme quand
// le journal finit ou que le contexte s'arrête.
type LogSource interface {
	OpenLogs(ctx context.Context, request LogRequest) (<-chan LogBatch, error)
}

// Commander pousse une commande à l'agent d'une machine par son flux ; il
// rend ErrMachineOffline quand elle est hors ligne.
type Commander interface {
	Command(machineID, name string, payload any) error
}

// Un lot attend ici tant que le navigateur ne l'a pas lu ; au-delà l'agent
// bloque sur son POST, ce qui freine la source.
const batchBacklog = 16

// Relay route une demande de journaux vers la bonne source et fait suivre
// les lots que l'agent livre à qui les attend.
type Relay struct {
	mu        sync.Mutex
	local     map[string]LogSource
	commander Commander
	pending   map[string]*follow
	// Le compte de suivis ouverts par machine, distants seulement : la
	// source locale se borne elle-même.
	open  map[string]int
	newID func() string
}

type follow struct {
	machineID    string
	batches      chan LogBatch
	done         chan struct{}
	closeDone    sync.Once
	closeBatches sync.Once
}

func NewRelay() *Relay {
	return &Relay{local: make(map[string]LogSource), pending: make(map[string]*follow), open: make(map[string]int), newID: newRequestID}
}

func (r *Relay) SetLocal(machineID string, source LogSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.local[machineID] = source
}

func (r *Relay) SetCommander(commander Commander) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commander = commander
}

// Open ouvre un journal ; l'identifiant de la requête est tiré ici, jamais
// reçu de l'agent ni du navigateur.
func (r *Relay) Open(ctx context.Context, machineID string, request LogRequest) (<-chan LogBatch, error) {
	request.ID = r.newID()
	r.mu.Lock()
	source, isLocal := r.local[machineID]
	r.mu.Unlock()
	if isLocal {
		return source.OpenLogs(ctx, request)
	}
	return r.openRemote(ctx, machineID, request)
}

func (r *Relay) openRemote(ctx context.Context, machineID string, request LogRequest) (<-chan LogBatch, error) {
	r.mu.Lock()
	if r.commander == nil {
		r.mu.Unlock()
		return nil, ErrMachineOffline
	}
	if r.open[machineID] >= MaxFollowsPerMachine {
		r.mu.Unlock()
		return nil, ErrLogsBusy
	}
	pending := &follow{machineID: machineID, batches: make(chan LogBatch, batchBacklog), done: make(chan struct{})}
	r.pending[request.ID] = pending
	r.open[machineID]++
	commander := r.commander
	r.mu.Unlock()

	if err := commander.Command(machineID, CommandLogs, request); err != nil {
		r.forget(request.ID)
		return nil, err
	}
	go r.stopWhenDone(ctx, request.ID, machineID, pending, commander)
	return pending.batches, nil
}

// stopWhenDone dit à l'agent d'arrêter dès que le lecteur s'en va, et
// ferme le canal quand l'agent a fini.
func (r *Relay) stopWhenDone(ctx context.Context, requestID, machineID string, pending *follow, commander Commander) {
	select {
	case <-ctx.Done():
		_ = commander.Command(machineID, CommandLogsStop, LogRequest{ID: requestID})
	case <-pending.done:
	}
	r.forget(requestID)
	pending.closeBatches.Do(func() { close(pending.batches) })
}

func (r *Relay) forget(requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.pending[requestID]
	if !ok {
		return
	}
	delete(r.pending, requestID)
	r.open[pending.machineID]--
}

// Deliver fait suivre un lot livré par l'agent ; faux si personne ne
// l'attend plus. Un lecteur qui ne lit pas fait attendre l'agent, jusqu'au
// contexte de son POST.
func (r *Relay) Deliver(ctx context.Context, requestID string, batch LogBatch) bool {
	r.mu.Lock()
	pending, ok := r.pending[requestID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case pending.batches <- batch:
	case <-ctx.Done():
		return false
	case <-pending.done:
		return false
	}
	if batch.Done || batch.Error != "" {
		pending.closeDone.Do(func() { close(pending.done) })
	}
	return true
}

func newRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

// Les journaux d'un service, par son relais.

func (t *Tracker) SetLocalLogSource(machineID string, source LogSource) {
	t.relay.SetLocal(machineID, source)
}

func (t *Tracker) SetCommander(commander Commander) {
	t.relay.SetCommander(commander)
}

// FollowLogs ouvre le journal d'un service en direct ; le canal se ferme
// avec le journal ou le contexte.
func (t *Tracker) FollowLogs(ctx context.Context, serviceID string, tail int) (<-chan LogBatch, error) {
	service, err := t.store.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	return t.relay.Open(ctx, service.MachineID, LogRequest{ContainerID: service.ContainerID, Tail: clampTail(tail), Follow: true})
}

// FetchLogs tire les dernières lignes en un coup ; passé le délai, la
// machine n'a pas répondu.
func (t *Tracker) FetchLogs(ctx context.Context, serviceID string, tail int) ([]LogLine, error) {
	service, err := t.store.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, LogFetchTimeout)
	defer cancel()
	batches, err := t.relay.Open(ctx, service.MachineID, LogRequest{ContainerID: service.ContainerID, Tail: clampTail(tail)})
	if err != nil {
		return nil, err
	}
	lines := []LogLine{}
	for {
		select {
		case <-ctx.Done():
			return nil, ErrLogsTimeout
		case batch, open := <-batches:
			if !open {
				return lines, nil
			}
			lines = append(lines, batch.Lines...)
			if batch.Error != "" {
				return nil, &LogsError{Code: batch.Error}
			}
			if batch.Done {
				return lines, nil
			}
		}
	}
}

// DeliverLogs reçoit un lot de l'agent ; faux si la requête est inconnue.
func (t *Tracker) DeliverLogs(ctx context.Context, requestID string, batch LogBatch) bool {
	return t.relay.Deliver(ctx, requestID, batch)
}

// LogsError est le refus d'un agent, avec son code.
type LogsError struct {
	Code string
}

func (e *LogsError) Error() string {
	return "logs failed: " + e.Code
}

func (e *LogsError) Is(target error) bool {
	return target == ErrLogsFailed
}

func clampTail(tail int) int {
	if tail <= 0 {
		return DefaultLogTail
	}
	return min(tail, MaxLogTail)
}

// Pour les tests : un identifiant prévisible.
func (r *Relay) setIDs(newID func() string) {
	r.newID = newID
}
