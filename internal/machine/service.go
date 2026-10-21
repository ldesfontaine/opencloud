package machine

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/deletion"
)

const (
	TokenLifetime = time.Hour
	maxClockSkew  = 5 * time.Minute
	// Une machine sans flux est perdue passé ce délai depuis son dernier
	// signal : quatre signaux manqués, pas un hoquet de réseau.
	LostAfter       = 2 * time.Minute
	lostCheckPeriod = 15 * time.Second
)

// Ce que le service attend de la base ; le package store le fournit.
type Store interface {
	ListMachines(ctx context.Context) ([]Machine, error)
	GetMachine(ctx context.Context, id string) (Machine, error)
	// DeleteMachine relit le plan dans sa transaction et refuse tant qu'il
	// est bloqué (RefuseDeletion) : un lien posé entre le plan et la
	// suppression ne passe pas.
	DeleteMachine(ctx context.Context, id string) error
	MachineDeletionPlan(ctx context.Context, id string) (deletion.Plan, error)
	TouchAgent(ctx context.Context, agentID string, at time.Time) error
	RecordConnection(ctx context.Context, connection Connection) error
	RecordSystem(ctx context.Context, id, system string) error
	RecordServerKey(ctx context.Context, agentID, fingerprint string) error
	InsertToken(ctx context.Context, token Token) error
	ListPendingTokens(ctx context.Context, now time.Time) ([]Token, error)
	DeleteToken(ctx context.Context, id string) error
	// Enroll consomme le jeton, crée la machine ou la retrouve, révoque son
	// agent actif et inscrit le nouveau, dans une seule transaction ; il
	// renvoie la machine telle qu'elle est en base.
	Enroll(ctx context.Context, tokenHash string, machine Machine, agent Agent, now time.Time) (Machine, error)
	// RevokeAgent révoque l'agent actif de la machine et annule ses jetons
	// de ré-enrôlement en attente, dans une seule transaction ; il rend
	// l'identifiant de l'agent révoqué, ErrNoAgent s'il n'y en a pas.
	RevokeAgent(ctx context.Context, machineID string, now time.Time) (string, error)
}

// Connection est ce que l'ouverture d'un flux note : l'adresse sur la
// machine, la version et les droits sur l'agent.
type Connection struct {
	MachineID string
	AgentID   string
	Address   string
	Version   string
	// nil : l'agent n'a rien rapporté (un agent d'avant les droits), ce qui
	// est en base reste.
	Capabilities []Capability
	At           time.Time
}

// Presence dit si l'agent d'une machine est joint. Le service ne sait pas
// d'où vient la réponse : serve la lit dans les flux ouverts, opencloud mcp,
// qui n'en tient aucun, dans le dernier signal de la base.
type Presence interface {
	IsOnline(machine Machine, now time.Time) bool
}

// Watcher reçoit chaque changement qui se voit dans l'interface : une
// machine qui entre, se connecte, part ; un jeton émis ou annulé. Le
// signal des 30 s n'en est pas un.
// Le direct s'y branche ; nil est toléré.
type Watcher interface {
	MachineChanged(machineID string)
}

// Alerter reçoit ce qui mérite une alerte : une machine perdue, revenue,
// retirée. Le moteur des alertes s'y branche ; nil est toléré.
type Alerter interface {
	MachineLost(ctx context.Context, machine Machine)
	MachineBack(ctx context.Context, machineID string)
	MachineRemoved(ctx context.Context, machineID string)
}

type Service struct {
	store      Store
	sessions   *Sessions
	presence   Presence
	challenges *challenges
	watcher    Watcher
	alerter    Alerter
	auditor    *audit.Recorder
	logger     *slog.Logger
	// La machine où tourne serve, et la ligne d'installation de son agent :
	// voir local.go.
	server    serverHost
	offerLock sync.Mutex
	offer     localOffer
	// now est remplaçable dans les tests : « vu il y a » se compare à lui.
	now func() time.Time
}

func New(store Store, sessions *Sessions, logger *slog.Logger) *Service {
	return &Service{
		store:      store,
		sessions:   sessions,
		presence:   openStreams{sessions: sessions},
		challenges: newChallenges(),
		logger:     logger,
		now:        time.Now,
	}
}

// SetClock remplace l'horloge, pour les tests et les rendus figés.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

// SetPresence remplace la façon de savoir qu'une machine est en ligne : les
// flux ouverts par défaut.
func (s *Service) SetPresence(presence Presence) {
	s.presence = presence
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

func (s *Service) SetAlerter(alerter Alerter) {
	s.alerter = alerter
}

func (s *Service) changed(machineID string) {
	if s.watcher != nil {
		s.watcher.MachineChanged(machineID)
	}
}

func (s *Service) List(ctx context.Context) ([]Status, error) {
	machines, err := s.store.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(machines))
	for _, machine := range machines {
		statuses = append(statuses, s.status(machine))
	}
	return statuses, nil
}

func (s *Service) Get(ctx context.Context, id string) (Status, error) {
	machine, err := s.store.GetMachine(ctx, id)
	if err != nil {
		return Status{}, err
	}
	return s.status(machine), nil
}

// Une machine est en ligne selon la présence installée : le flux ouvert de
// son agent, ou son dernier signal. La machine openCloud comme les autres.
func (s *Service) status(machine Machine) Status {
	return Status{Machine: machine, Online: s.presence.IsOnline(machine, s.now())}
}

// Count renvoie le nombre de machines et celles en ligne.
func (s *Service) Count(ctx context.Context) (total, online int, err error) {
	statuses, err := s.List(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, status := range statuses {
		if status.Online {
			online++
		}
	}
	return len(statuses), online, nil
}

// CreateToken émet le jeton d'une nouvelle machine, nommée par l'opérateur,
// avec les droits qu'il a cochés ; observe s'y ajoute toujours.
func (s *Service) CreateToken(ctx context.Context, name string, capabilities []Capability) (string, Token, error) {
	if err := ValidateName(name); err != nil {
		return "", Token{}, err
	}
	normalized, err := NormalizeCapabilities(capabilities)
	if err != nil {
		return "", Token{}, err
	}
	return s.issueToken(ctx, name, "", normalized)
}

// CreateReenrollToken émet un jeton qui installe un nouvel agent sur une
// machine existante : elle garde son id et son historique, l'agent d'avant
// est révoqué à l'enrôlement. C'est aussi la seule façon de changer les
// droits d'un agent (décision 10) : capabilities les donne ; nil reprend ce
// que le jeton du précédent demandait, observe seul s'il n'en demandait
// aucun.
func (s *Service) CreateReenrollToken(ctx context.Context, machineID string, capabilities []Capability) (string, Token, error) {
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return "", Token{}, err
	}
	if capabilities == nil {
		capabilities = machine.Agent.Requested
	}
	normalized, err := NormalizeCapabilities(capabilities)
	if err != nil {
		return "", Token{}, err
	}
	return s.issueToken(ctx, machine.Name, machine.ID, normalized)
}

func (s *Service) issueToken(ctx context.Context, name, machineID string, capabilities []Capability) (string, Token, error) {
	cleartext, token, err := NewToken(name, machineID)
	if err != nil {
		return "", Token{}, fmt.Errorf("generate token: %w", err)
	}
	token.Capabilities = capabilities
	now := s.now()
	token.CreatedAt = now
	token.ExpiresAt = now.Add(TokenLifetime)
	if err := s.store.InsertToken(ctx, token); err != nil {
		return "", Token{}, fmt.Errorf("insert token: %w", err)
	}
	s.auditor.Record(ctx, tokenEvent(token))
	s.changed(machineID)
	return cleartext, token, nil
}

func (s *Service) PendingTokens(ctx context.Context) ([]Token, error) {
	return s.store.ListPendingTokens(ctx, s.now())
}

func (s *Service) CancelToken(ctx context.Context, id string) error {
	if err := s.store.DeleteToken(ctx, id); err != nil {
		return err
	}
	s.auditor.Record(ctx, audit.Event{Action: audit.EnrollmentTokenCancel, ObjectID: id})
	s.changed("")
	return nil
}

// DeletePlan dit ce que Remove ferait : ce qui le retient (services, sondes,
// tâches, membres de la page de statut), ce qui part avec la machine et ce
// qui s'en détache. Rien n'est écrit.
func (s *Service) DeletePlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := s.store.GetMachine(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return s.store.MachineDeletionPlan(ctx, id)
}

// Remove retire une machine et coupe son flux, la machine openCloud comme les
// autres. Tant que son plan est bloqué, le refus porte ce plan.
func (s *Service) Remove(ctx context.Context, id string) error {
	machine, err := s.store.GetMachine(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteMachine(ctx, id); err != nil {
		return err
	}
	s.sessions.Close(id)
	s.logger.Info("machine removed", "machine_id", id, "name", machine.Name)
	s.auditor.Record(ctx, machineEvent(audit.MachineDelete, machine))
	if s.alerter != nil {
		s.alerter.MachineRemoved(ctx, id)
	}
	s.changed(id)
	return nil
}

// CheckLost signale au moteur des alertes chaque machine sans flux dont
// le dernier signal date de plus de LostAfter. Sans état en mémoire :
// le moteur déduplique, et une machine tombée pendant que le serveur
// était éteint est vue au premier passage.
func (s *Service) CheckLost(ctx context.Context) error {
	if s.alerter == nil {
		return nil
	}
	statuses, err := s.List(ctx)
	if err != nil {
		return err
	}
	limit := s.now().Add(-LostAfter)
	for _, status := range statuses {
		if status.Online || status.LastSeenAt.After(limit) {
			continue
		}
		// Sans agent actif, rien à perdre : la machine attend son agent, ou
		// l'opérateur l'a révoqué.
		if !status.Agent.IsActive() {
			continue
		}
		s.alerter.MachineLost(ctx, status.Machine)
	}
	return nil
}

// Watch est la boucle de fond : les machines perdues, toutes les 15 s.
func (s *Service) Watch(ctx context.Context) {
	ticker := time.NewTicker(lostCheckPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.CheckLost(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("check lost machines", "error", err)
			}
		}
	}
}

// Enroll vérifie ce que l'agent envoie puis laisse la base consommer le jeton
// et écrire la machine d'un seul coup.
func (s *Service) Enroll(ctx context.Context, request Enrollment) (Machine, error) {
	if err := ValidateID(request.MachineID); err != nil {
		return Machine{}, err
	}
	if len(request.PublicKey) != ed25519.PublicKeySize {
		return Machine{}, ErrBadKey
	}
	agentID, err := NewAgentID()
	if err != nil {
		return Machine{}, fmt.Errorf("generate agent id: %w", err)
	}
	now := s.now()
	candidate := Machine{
		ID:        s.machineIDFor(request),
		Hostname:  request.Hostname,
		Address:   request.Address,
		OS:        request.OS,
		Arch:      request.Arch,
		CreatedAt: now,
	}
	agent := Agent{ID: agentID, PublicKey: request.PublicKey, Version: request.AgentVersion, EnrolledAt: now}
	machine, err := s.store.Enroll(ctx, HashToken(request.Token), candidate, agent, now)
	if err != nil {
		return Machine{}, err
	}
	// Un ré-enrôlement révoque l'agent d'avant : son flux ne vaut plus rien.
	s.sessions.Close(machine.ID)
	s.logger.Info("agent enrolled", "machine_id", machine.ID, "agent_id", machine.Agent.ID, "name", machine.Name)
	s.auditor.Record(ctx, agentEvent(audit.AgentEnroll, machine))
	s.changed(machine.ID)
	return machine, nil
}

// Revoke révoque l'agent actif d'une machine : il est refusé à sa prochaine
// preuve, son flux est coupé, les jetons de ré-enrôlement en attente sont
// annulés. La machine, son historique et ses services restent ; ré-enrôler
// installe un nouvel agent et lève la révocation.
func (s *Service) Revoke(ctx context.Context, machineID string) error {
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return err
	}
	now := s.now()
	revokedID, err := s.store.RevokeAgent(ctx, machineID, now)
	if err != nil {
		return err
	}
	s.sessions.Close(machineID)
	machine.Agent.ID, machine.Agent.RevokedAt = revokedID, now
	s.logger.Info("agent revoked", "machine_id", machineID, "agent_id", revokedID, "name", machine.Name)
	s.auditor.Record(ctx, agentEvent(audit.AgentRevoke, machine))
	s.changed(machineID)
	return nil
}

// Challenge émet le défi qu'un agent devra signer pour ouvrir son flux.
func (s *Service) Challenge(ctx context.Context, machineID string) ([]byte, error) {
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return nil, err
	}
	// Un agent révoqué reçoit encore son défi : c'est sa preuve qui lui
	// dira qu'il est révoqué.
	if !machine.Agent.Exists() {
		return nil, ErrNotFound
	}
	return s.challenges.issue(machineID, s.now()), nil
}

// Ce que l'agent présente pour ouvrir son flux.
type Proof struct {
	MachineID string
	Nonce     []byte
	Timestamp int64
	Signature []byte
}

// Authenticate vérifie la preuve : défi signé par ce serveur pour cette
// machine et pas expiré, horloge dans la tolérance, signature de la clé de
// son agent. Le défi n'est marqué utilisé qu'une fois la signature vérifiée :
// quelqu'un qui n'a pas la clé ne peut pas brûler le défi d'un autre.
// ErrBadNonce (défi absent, expiré, d'un autre serveur ou déjà utilisé) se
// répare en redemandant un défi ; ErrBadSignature, non. Un agent révoqué
// qui prouve sa clé apprend qu'il l'est (ErrAgentRevoked) ; sans sa clé, il
// n'apprend rien de plus qu'un autre.
func (s *Service) Authenticate(ctx context.Context, proof Proof) (Machine, error) {
	machine, err := s.store.GetMachine(ctx, proof.MachineID)
	if err != nil {
		return Machine{}, err
	}
	if !machine.Agent.Exists() {
		return Machine{}, ErrNotFound
	}
	now := s.now()
	if !s.challenges.valid(proof.Nonce, proof.MachineID, now) {
		return Machine{}, ErrBadNonce
	}
	skew := now.Sub(time.Unix(proof.Timestamp, 0))
	if skew > maxClockSkew || skew < -maxClockSkew {
		return Machine{}, ErrClockSkew
	}
	payload, err := SignedPayload(proof.Nonce, proof.MachineID, proof.Timestamp)
	if err != nil {
		return Machine{}, err
	}
	if !Verify(machine.Agent.PublicKey, payload, proof.Signature) {
		return Machine{}, ErrBadSignature
	}
	if !s.challenges.markUsed(proof.Nonce, now) {
		return Machine{}, ErrBadNonce
	}
	if machine.Agent.IsRevoked() {
		return Machine{}, ErrAgentRevoked
	}
	return machine, nil
}

// Connect ouvre la session d'une machine authentifiée et note sa venue :
// l'adresse sur la machine, la version et les droits sur son agent. Des
// droits illisibles ne sont pas gardés, le flux s'ouvre quand même.
func (s *Service) Connect(ctx context.Context, authenticated Machine, address, agentVersion, capabilities string) (*Session, error) {
	machineID := authenticated.ID
	now := s.now()
	connection := Connection{
		MachineID: machineID, AgentID: authenticated.Agent.ID,
		Address: address, Version: agentVersion, At: now,
	}
	if capabilities != "" {
		reported, err := ParseCapabilities(capabilities)
		if err != nil {
			s.logger.Warn("agent capabilities unreadable", "machine_id", machineID, "error", err)
		}
		connection.Capabilities = reported
	}
	if err := s.store.RecordConnection(ctx, connection); err != nil {
		return nil, fmt.Errorf("record connection: %w", err)
	}
	session, err := s.sessions.Open(machineID, authenticated.Agent.ID, now)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	s.logger.Info("machine connected", "machine_id", machineID, "address", address)
	if s.alerter != nil {
		s.alerter.MachineBack(ctx, machineID)
	}
	s.changed(machineID)
	return session, nil
}

// NoteServerKey garde l'empreinte de la clé du serveur que l'agent dit avoir
// épinglée, redite à chaque ouverture de flux : la fiche montre un agent qui
// ne vérifierait pas les ordres de ce serveur. Une forme qui n'est pas une
// empreinte, ou rien, s'écrit vide.
func (s *Service) NoteServerKey(ctx context.Context, agentID, fingerprint string) error {
	if !isKeyFingerprint(fingerprint) {
		fingerprint = ""
	}
	return s.store.RecordServerKey(ctx, agentID, fingerprint)
}

// isKeyFingerprint : « SHA256: » et 43 caractères de base64 sans
// remplissage, la forme d'agentwire.KeyFingerprint.
func isKeyFingerprint(text string) bool {
	digest, found := strings.CutPrefix(text, "SHA256:")
	if !found || len(digest) != 43 {
		return false
	}
	_, err := base64.RawStdEncoding.Strict().DecodeString(digest)
	return err == nil
}

// MaxSystemLength borne ce qu'un agent dit de son système.
const MaxSystemLength = 120

// NoteSystem tient à jour le système d'une machine, dit par son agent à
// l'ouverture du flux. Un agent plus vieux ne le dit pas, et un texte qui
// ne ressemble pas à un nom de système n'est pas gardé : on reste sur ce
// que l'enrôlement a noté.
func (s *Service) NoteSystem(ctx context.Context, machineID, system string) error {
	if !IsSystemName(system) {
		return nil
	}
	return s.store.RecordSystem(ctx, machineID, system)
}

// IsSystemName : du texte ASCII imprimable, sur une ligne, de longueur
// bornée. C'est aussi ce qui passe dans un en-tête : l'agent n'envoie rien
// d'autre.
func IsSystemName(system string) bool {
	if system == "" || len(system) > MaxSystemLength {
		return false
	}
	for _, character := range system {
		if character < ' ' || character > '~' {
			return false
		}
	}
	return true
}

// Systems rend le système de chaque machine, tel que son agent le nomme.
func (s *Service) Systems(ctx context.Context) ([]string, error) {
	machines, err := s.store.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	systems := make([]string, 0, len(machines))
	for _, machine := range machines {
		systems = append(systems, machine.OS)
	}
	return systems, nil
}

// Disconnect libère la session quand son flux se termine.
func (s *Service) Disconnect(session *Session) {
	s.sessions.Release(session)
	s.logger.Info("machine disconnected", "machine_id", session.MachineID)
	s.changed(session.MachineID)
}

// Signal note qu'une machine dont le flux est ouvert vient de donner signe
// de vie, et rend son identifiant : ce que le signal porte avec lui, les
// échantillons de ressources, s'écrit à ce nom. Il ne publie rien : un
// signal n'arrive que sur un flux ouvert, la machine est déjà en ligne et
// Connect l'a dit, avec son système et sa version ; seul le dernier signal
// bouge, et l'écran n'en a pas besoin en direct.
func (s *Service) Signal(ctx context.Context, sessionToken string) (string, error) {
	session, ok := s.sessions.Lookup(sessionToken)
	if !ok {
		return "", ErrNotConnected
	}
	if err := s.store.TouchAgent(ctx, session.AgentID, s.now()); err != nil {
		return "", err
	}
	return session.MachineID, nil
}

// SessionMachine rend la machine d'une session ouverte, sans rien écrire
// ni publier : un lot de journaux n'est pas un signe de vie, le signal des
// 30 s s'en charge.
func (s *Service) SessionMachine(sessionToken string) (string, error) {
	session, ok := s.sessions.Lookup(sessionToken)
	if !ok {
		return "", ErrNotConnected
	}
	return session.MachineID, nil
}

// Push pousse un événement à l'agent d'une machine par son flux ouvert.
func (s *Service) Push(machineID string, event StreamEvent) error {
	return s.sessions.Push(machineID, event)
}
