package restic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Les bornes du relais (limite n° 12 de l'essai) : au-delà de la file, 503 ;
// au-delà du délai, 504. restic réessaie l'un et l'autre, le mandataire de la
// source borne sa patience.
const (
	defaultQueuePerTarget = 256
	defaultHeadTimeout    = 2 * time.Minute
	relayIDRandomBytes    = 16
)

var (
	ErrBadProof       = errors.New("relay: proof refused")
	ErrNotLinked      = errors.New("relay: source not linked to this target")
	ErrTargetBusy     = errors.New("relay: target queue full")
	ErrTargetOffline  = errors.New("relay: target not connected")
	ErrTargetTimeout  = errors.New("relay: target did not answer in time")
	ErrUnknownRequest = errors.New("relay: no such pending request")
	errBodyDetached   = errors.New("relay: source request finished")
)

// Authenticator vérifie la preuve signée que la source pose sur chaque
// requête : signature, horloge, et couple (machine, aléa) jamais servi.
// machine.Service le fait déjà pour l'ouverture du flux.
type Authenticator interface {
	Authenticate(ctx context.Context, proof machine.Proof) (machine.Machine, error)
}

// Links rend le dépôt d'une source sur une cible, d'après le lien déclaré par
// l'opérateur ; ErrNotLinked sans lien. C'est le serveur qui nomme le dépôt,
// jamais la source.
type Links interface {
	RelayRepo(ctx context.Context, sourceID, targetID string) (string, error)
}

// Pusher pousse la requête à l'agent de la cible par son flux ; il refuse
// sans flux (machine.ErrNotConnected) ou file pleine (machine.ErrBusy).
type Pusher interface {
	PushResticRelay(targetID string, request agentwire.ResticRelayRequest) error
}

// BrokerLimits ; zéro prend la valeur de l'essai.
type BrokerLimits struct {
	QueuePerTarget int
	HeadTimeout    time.Duration
}

// RelayRequest est la requête de restic telle que la source l'a envoyée :
// le chemin relatif au dépôt, encodé, et le corps au fil de l'eau.
type RelayRequest struct {
	Method        string
	Path          string
	Query         string
	Accept        string
	Range         string
	ContentLength int64
	Body          io.Reader
}

// RelayResponse est la réponse de rest-server, corps au fil de l'eau. Close
// libère la place dans la file : à appeler une fois le corps lu.
type RelayResponse struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// Broker est le relais côté serveur : il tient les requêtes en attente par
// cible, donne le corps à la cible qui le tire, rend la réponse qu'elle
// pousse. Rien n'est stocké : tout passe par des tuyaux.
type Broker struct {
	authenticator Authenticator
	links         Links
	pusher        Pusher
	logger        *slog.Logger
	limits        BrokerLimits

	mu       sync.Mutex
	pending  map[string]*pendingRequest
	byTarget map[string]int
}

// pendingRequest : le corps de la source derrière une barrière, et le tuyau
// où la cible écrit la réponse.
type pendingRequest struct {
	target      string
	body        *detachableReader
	bodyTaken   bool
	delivered   bool
	head        chan agentwire.ResticRelayHead
	reply       *io.PipeReader
	replyWriter *io.PipeWriter
}

func NewBroker(authenticator Authenticator, links Links, pusher Pusher, logger *slog.Logger) *Broker {
	return &Broker{
		authenticator: authenticator,
		links:         links,
		pusher:        pusher,
		logger:        logger,
		limits:        BrokerLimits{QueuePerTarget: defaultQueuePerTarget, HeadTimeout: defaultHeadTimeout},
		pending:       make(map[string]*pendingRequest),
		byTarget:      make(map[string]int),
	}
}

// SetPusher branche le flux des cibles : le serveur, qui tient les flux et
// naît après le courtier. Avant le démarrage.
func (b *Broker) SetPusher(pusher Pusher) {
	b.pusher = pusher
}

// SetLimits remplace les bornes, avant le démarrage.
func (b *Broker) SetLimits(limits BrokerLimits) {
	if limits.QueuePerTarget > 0 {
		b.limits.QueuePerTarget = limits.QueuePerTarget
	}
	if limits.HeadTimeout > 0 {
		b.limits.HeadTimeout = limits.HeadTimeout
	}
}

// Submit authentifie la source, juge la requête, la pousse à la cible et
// attend la tête de la réponse. Le dépôt vient du lien, pas de la requête.
func (b *Broker) Submit(ctx context.Context, proof machine.Proof, targetID string, request RelayRequest) (RelayResponse, error) {
	source, err := b.authenticator.Authenticate(ctx, proof)
	if err != nil {
		return RelayResponse{}, fmt.Errorf("%w: %w", ErrBadProof, err)
	}
	if err := Allow(request.Method, request.Path, request.Query); err != nil {
		b.logger.Info("restic relay refused", "source", source.ID, "target", targetID, "method", request.Method, "refusal", err.Error())
		return RelayResponse{}, err
	}
	repo, err := b.links.RelayRepo(ctx, source.ID, targetID)
	if err != nil {
		return RelayResponse{}, fmt.Errorf("resolve relay repo: %w", err)
	}
	id, pending, err := b.open(targetID, request.Body)
	if err != nil {
		return RelayResponse{}, err
	}
	event := agentwire.ResticRelayRequest{
		ID: id, Repo: repo, Method: request.Method, Path: request.Path, Query: request.Query,
		Accept: request.Accept, Range: request.Range, ContentLength: request.ContentLength,
	}
	if err := event.Validate(); err != nil {
		b.finish(id)
		return RelayResponse{}, fmt.Errorf("build relay event: %w", err)
	}
	if err := b.push(targetID, event); err != nil {
		b.finish(id)
		return RelayResponse{}, err
	}
	return b.awaitHead(ctx, id, pending)
}

// open réserve une place dans la file de la cible.
func (b *Broker) open(targetID string, body io.Reader) (string, *pendingRequest, error) {
	id, err := newRelayID()
	if err != nil {
		return "", nil, err
	}
	if body == nil {
		body = http.NoBody
	}
	reply, replyWriter := io.Pipe()
	pending := &pendingRequest{
		target:      targetID,
		body:        &detachableReader{reader: body},
		head:        make(chan agentwire.ResticRelayHead, 1),
		reply:       reply,
		replyWriter: replyWriter,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byTarget[targetID] >= b.limits.QueuePerTarget {
		return "", nil, ErrTargetBusy
	}
	b.byTarget[targetID]++
	b.pending[id] = pending
	return id, pending, nil
}

func (b *Broker) push(targetID string, event agentwire.ResticRelayRequest) error {
	err := b.pusher.PushResticRelay(targetID, event)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, machine.ErrBusy):
		return ErrTargetBusy
	default:
		return fmt.Errorf("%w: %w", ErrTargetOffline, err)
	}
}

func (b *Broker) awaitHead(ctx context.Context, id string, pending *pendingRequest) (RelayResponse, error) {
	timer := time.NewTimer(b.limits.HeadTimeout)
	defer timer.Stop()
	select {
	case head := <-pending.head:
		return RelayResponse{
			Status: head.Status,
			Header: relayedHeaders(head.Headers),
			Body:   &relayBody{reader: pending.reply, finish: func() { b.finish(id) }},
		}, nil
	case <-timer.C:
		b.finish(id)
		return RelayResponse{}, ErrTargetTimeout
	case <-ctx.Done():
		b.finish(id)
		return RelayResponse{}, ctx.Err()
	}
}

// TakeBody rend le corps de la requête à la cible qui la sert, une fois.
// Une autre machine, ou un second tirage, n'apprend rien de plus qu'un
// identifiant inconnu.
func (b *Broker) TakeBody(targetID, id string) (io.Reader, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending, ok := b.pending[id]
	if !ok || pending.target != targetID || pending.bodyTaken {
		return nil, ErrUnknownRequest
	}
	pending.bodyTaken = true
	return pending.body, nil
}

// Deliver rend la réponse de la cible à la source, au fil de l'eau : il
// rend la main quand la source a tout lu, ou qu'elle est partie.
func (b *Broker) Deliver(targetID, id string, head agentwire.ResticRelayHead, body io.Reader) error {
	pending, err := b.claimDelivery(targetID, id)
	if err != nil {
		return err
	}
	if head.Status < 100 || head.Status > 599 {
		head = agentwire.ResticRelayHead{Status: http.StatusBadGateway}
	}
	pending.head <- head
	_, err = io.Copy(pending.replyWriter, body)
	_ = pending.replyWriter.CloseWithError(err)
	if err != nil {
		return fmt.Errorf("relay response body: %w", err)
	}
	return nil
}

func (b *Broker) claimDelivery(targetID, id string) (*pendingRequest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pending, ok := b.pending[id]
	if !ok || pending.target != targetID || pending.delivered {
		return nil, ErrUnknownRequest
	}
	pending.delivered = true
	return pending, nil
}

// finish libère la place et coupe les deux tuyaux : le corps de la source ne
// se lit plus (son handler va rendre la main), une réponse encore en route
// échoue au lieu d'attendre un lecteur parti.
func (b *Broker) finish(id string) {
	b.mu.Lock()
	pending, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
		b.byTarget[pending.target]--
		if b.byTarget[pending.target] == 0 {
			delete(b.byTarget, pending.target)
		}
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	pending.body.detach()
	_ = pending.reply.Close()
}

func newRelayID() (string, error) {
	raw := make([]byte, relayIDRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("draw relay id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// relayedHeaders ne garde que les en-têtes de réponse de la liste.
func relayedHeaders(headers map[string]string) http.Header {
	kept := http.Header{}
	for _, name := range agentwire.ResticRelayResponseHeaders() {
		if value, ok := headers[name]; ok {
			kept.Set(name, value)
		}
	}
	return kept
}

// detachableReader garde le corps de la source derrière une barrière : une
// fois détaché, la cible ne le lit plus, car le handler de la source a pu
// rendre la main et son corps n'est plus à lire.
type detachableReader struct {
	mu         sync.Mutex
	reader     io.Reader
	isDetached bool
}

func (d *detachableReader) Read(buffer []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isDetached {
		return 0, errBodyDetached
	}
	return d.reader.Read(buffer)
}

func (d *detachableReader) detach() {
	d.mu.Lock()
	d.isDetached = true
	d.mu.Unlock()
}

// relayBody ferme le tuyau de la réponse et libère la requête, une fois.
type relayBody struct {
	reader    *io.PipeReader
	finish    func()
	closeOnce sync.Once
}

func (r *relayBody) Read(buffer []byte) (int, error) {
	return r.reader.Read(buffer)
}

func (r *relayBody) Close() error {
	r.closeOnce.Do(r.finish)
	return nil
}

// RelayStatus traduit une erreur du relais en statut et code pour la
// source. restic s'arrête au premier 401, 403 ou 404 et réessaie un 5xx.
func RelayStatus(err error) (int, string) {
	var refusal Refusal
	switch {
	case errors.As(err, &refusal):
		return http.StatusForbidden, string(refusal)
	case errors.Is(err, ErrBadProof):
		return http.StatusUnauthorized, "bad_proof"
	case errors.Is(err, ErrNotLinked):
		return http.StatusForbidden, "link_refused"
	case errors.Is(err, ErrTargetBusy):
		return http.StatusServiceUnavailable, "target_busy"
	case errors.Is(err, ErrTargetOffline):
		return http.StatusServiceUnavailable, "target_offline"
	case errors.Is(err, ErrTargetTimeout):
		return http.StatusGatewayTimeout, "target_timeout"
	case errors.Is(err, ErrUnknownRequest):
		return http.StatusGone, "request_gone"
	}
	return http.StatusBadGateway, "relay_failed"
}
