package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"unicode/utf8"
)

// LogSource sait ouvrir un journal sur sa machine : c'est l'agent, qui lit
// son Docker. Le canal se ferme quand le journal finit ou que le contexte
// s'arrête.
type LogSource interface {
	OpenLogs(ctx context.Context, request LogRequest) (<-chan LogBatch, error)
}

// Commander demande à l'agent d'une machine, par son flux, d'ouvrir ou de
// fermer un suivi de journal ; il rend ErrMachineOffline quand elle est
// hors ligne. Le nom de l'événement est au protocole (agentwire), pas ici.
type Commander interface {
	RequestLogs(machineID string, request LogRequest) error
	StopLogs(machineID, requestID string) error
}

// Un lot attend ici tant que le navigateur ne l'a pas lu ; au-delà l'agent
// bloque sur son POST, ce qui freine la source.
const batchBacklog = 16

// Relay demande un journal à l'agent d'une machine et fait suivre les lots
// qu'il livre à qui les attend.
type Relay struct {
	mu        sync.Mutex
	commander Commander
	pending   map[string]*follow
	// Le compte de suivis ouverts par machine.
	open  map[string]int
	newID func() string
}

type follow struct {
	machineID string
	batches   chan LogBatch
	done      chan struct{}
	closeDone sync.Once
	// Un envoi sur batches se fait sous verrou partagé, la fermeture sous
	// verrou exclusif : jamais d'envoi sur un canal fermé.
	sending  sync.RWMutex
	isClosed bool
}

func (f *follow) markDone() {
	f.closeDone.Do(func() { close(f.done) })
}

func NewRelay() *Relay {
	return &Relay{pending: make(map[string]*follow), open: make(map[string]int), newID: newRequestID}
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
	if r.commander == nil {
		r.mu.Unlock()
		return nil, ErrLogsUnavailableLocally
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

	if err := commander.RequestLogs(machineID, request); err != nil {
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
		_ = commander.StopLogs(machineID, requestID)
	case <-pending.done:
	}
	r.forget(requestID)
	// done d'abord : un Deliver bloqué sur un lecteur parti lâche son
	// verrou partagé, sans quoi la fermeture l'attendrait.
	pending.markDone()
	pending.sending.Lock()
	pending.isClosed = true
	close(pending.batches)
	pending.sending.Unlock()
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

// Deliver fait suivre un lot livré par l'agent. ErrLogRequestUnknown si
// personne ne l'attend plus, ou si la requête n'a pas été poussée à cette
// machine ; ErrLogBatchTooLarge si le lot sort de ce qu'un agent produit.
// Une ligne trop longue est coupée, pas refusée. Un lecteur qui ne lit pas
// fait attendre l'agent, jusqu'au contexte de son POST.
func (r *Relay) Deliver(ctx context.Context, machineID, requestID string, batch LogBatch) error {
	r.mu.Lock()
	pending, ok := r.pending[requestID]
	r.mu.Unlock()
	if !ok || pending.machineID != machineID {
		return ErrLogRequestUnknown
	}
	// Après la requête connue : un lot hors normes n'apprend rien à qui ne
	// sait pas déjà quelle requête on lui a poussée.
	if err := checkLogBatch(batch); err != nil {
		return err
	}
	clipLongLines(batch)
	if !pending.send(ctx, batch) {
		return ErrLogRequestUnknown
	}
	if batch.Done || batch.Error != "" {
		pending.markDone()
	}
	return nil
}

// checkLogBatch refuse ce qu'aucun agent ne produit, même ancien : plus de
// MaxLinesPerBatch lignes, un nom de flux plus long que « stdout ». Le
// serveur ne se fie pas à la borne que l'agent s'applique : chaque ligne
// reçue est retenue le temps de la demande.
func checkLogBatch(batch LogBatch) error {
	if len(batch.Lines) > MaxLinesPerBatch {
		return ErrLogBatchTooLarge
	}
	for _, line := range batch.Lines {
		if len(line.Stream) > maxLogStreamBytes {
			return ErrLogBatchTooLarge
		}
	}
	return nil
}

// clipLongLines coupe chaque ligne à MaxLogLineBytes, sur une limite de
// caractère. Couper plutôt que refuser : le lecteur de Docker des agents
// installés rend une ligne multiplexée un peu au-delà de 64 Kio et ne coupe
// pas celle d'un terminal ; un refus arrêterait leur suivi, la coupe borne
// la mémoire tout autant. Le décodeur JSON rend déjà de l'UTF-8 valide.
func clipLongLines(batch LogBatch) {
	for i, line := range batch.Lines {
		if len(line.Text) <= MaxLogLineBytes {
			continue
		}
		cut := MaxLogLineBytes
		for cut > 0 && !utf8.RuneStart(line.Text[cut]) {
			cut--
		}
		batch.Lines[i].Text = line.Text[:cut]
	}
}

func (f *follow) send(ctx context.Context, batch LogBatch) bool {
	f.sending.RLock()
	defer f.sending.RUnlock()
	if f.isClosed {
		return false
	}
	select {
	case f.batches <- batch:
		return true
	case <-ctx.Done():
		return false
	case <-f.done:
		return false
	}
}

func newRequestID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}

// Les journaux d'un conteneur, par son relais.

func (t *Tracker) SetCommander(commander Commander) {
	t.relay.SetCommander(commander)
}

// FollowLogs ouvre le journal d'un conteneur en direct ; le canal se ferme
// avec le journal ou le contexte.
func (t *Tracker) FollowLogs(ctx context.Context, containerID string, tail int) (<-chan LogBatch, error) {
	container, err := t.store.GetContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return t.relay.Open(ctx, container.MachineID, LogRequest{ContainerID: container.ContainerID, Tail: ClampLogTail(tail), Follow: true})
}

// FetchLogs tire les dernières lignes en un coup ; passé le délai, la
// machine n'a pas répondu.
func (t *Tracker) FetchLogs(ctx context.Context, containerID string, tail int) ([]LogLine, error) {
	container, err := t.store.GetContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, LogFetchTimeout)
	defer cancel()
	batches, err := t.relay.Open(ctx, container.MachineID, LogRequest{ContainerID: container.ContainerID, Tail: ClampLogTail(tail)})
	if err != nil {
		return nil, err
	}
	// Seules les dernières lignes demandées sont gardées, quoi que l'agent
	// envoie : un agent honnête n'en envoie pas plus, un autre ne remplit pas
	// la mémoire du serveur pendant les 15 s. Passé MaxFetchLogBytes reçus, la
	// demande est coupée (le contexte annulé dit à l'agent d'arrêter) et ce
	// qui est gardé est rendu.
	limit := ClampLogTail(tail)
	lines := []LogLine{}
	received := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ErrLogsTimeout
		case batch, open := <-batches:
			if !open {
				return lines, nil
			}
			lines = keepLastLines(lines, batch.Lines, limit)
			for _, line := range batch.Lines {
				received += len(line.Text) + len(line.Stream)
			}
			if batch.Error != "" {
				return nil, &LogsError{Code: batch.Error}
			}
			if batch.Done || received > MaxFetchLogBytes {
				return lines, nil
			}
		}
	}
}

// keepLastLines ajoute des lignes et ne garde que les limit dernières. La
// coupe recopie : sans quoi le tableau d'origine, plein de lignes écartées,
// resterait retenu.
func keepLastLines(kept, added []LogLine, limit int) []LogLine {
	kept = append(kept, added...)
	if len(kept) > limit {
		kept = append([]LogLine(nil), kept[len(kept)-limit:]...)
	}
	return kept
}

// DeliverLogs reçoit un lot de l'agent ; voir Relay.Deliver pour ses refus.
func (t *Tracker) DeliverLogs(ctx context.Context, machineID, requestID string, batch LogBatch) error {
	return t.relay.Deliver(ctx, machineID, requestID, batch)
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

// ClampLogTail borne le nombre de lignes demandé : le serveur et l'agent
// bornent la même chose.
func ClampLogTail(tail int) int {
	if tail <= 0 {
		return DefaultLogTail
	}
	return min(tail, MaxLogTail)
}
