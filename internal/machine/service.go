package machine

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"time"
)

const (
	TokenLifetime    = 24 * time.Hour
	maxClockSkew     = 5 * time.Minute
	localMachineName = "opencloud"
)

// Ce que le service attend de la base ; le package store le fournit.
type Store interface {
	SaveLocalMachine(ctx context.Context, machine Machine) error
	ListMachines(ctx context.Context) ([]Machine, error)
	GetMachine(ctx context.Context, id string) (Machine, error)
	DeleteMachine(ctx context.Context, id string) error
	TouchMachine(ctx context.Context, id string, at time.Time) error
	RecordConnection(ctx context.Context, id, address, agentVersion string, at time.Time) error
	InsertToken(ctx context.Context, token Token) error
	ListPendingTokens(ctx context.Context, now time.Time) ([]Token, error)
	DeleteToken(ctx context.Context, id string) error
	// Enroll consomme le jeton et crée ou ré-enrôle la machine dans une seule
	// transaction ; il renvoie la machine telle qu'elle est en base.
	Enroll(ctx context.Context, tokenHash string, machine Machine, now time.Time) (Machine, error)
}

// Ce que le serveur sait de la machine où il tourne.
type LocalInfo struct {
	Hostname string
	Address  string
	OS       string
	Arch     string
	Version  string
}

// Listener reçoit chaque changement qui se voit dans l'interface : une
// machine qui entre, se connecte, signale, part ; un jeton émis ou annulé.
// Le direct s'y branche ; nil est toléré.
type Listener interface {
	MachineChanged(machineID string)
}

type Service struct {
	store    Store
	sessions *Sessions
	nonces   *nonces
	listener Listener
	logger   *slog.Logger
	// now est remplaçable dans les tests : « vu il y a » se compare à lui.
	now func() time.Time
}

func New(store Store, sessions *Sessions, logger *slog.Logger) *Service {
	return &Service{
		store:    store,
		sessions: sessions,
		nonces:   newNonces(),
		logger:   logger,
		now:      time.Now,
	}
}

// SetClock remplace l'horloge, pour les tests et les rendus figés.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetListener(listener Listener) {
	s.listener = listener
}

func (s *Service) changed(machineID string) {
	if s.listener != nil {
		s.listener.MachineChanged(machineID)
	}
}

// EnsureLocal crée ou rafraîchit la machine openCloud elle-même au démarrage.
func (s *Service) EnsureLocal(ctx context.Context, info LocalInfo) error {
	now := s.now()
	machine := Machine{
		ID:           LocalID,
		Name:         localMachineName,
		Kind:         KindLocal,
		Hostname:     info.Hostname,
		Address:      info.Address,
		OS:           info.OS,
		Arch:         info.Arch,
		AgentVersion: info.Version,
		EnrolledAt:   now,
		LastSeenAt:   now,
		CreatedAt:    now,
	}
	if err := s.store.SaveLocalMachine(ctx, machine); err != nil {
		return fmt.Errorf("save local machine: %w", err)
	}
	return nil
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

// La machine openCloud est en ligne tant que ce processus tourne ; les autres
// le sont tant que leur agent tient son flux.
func (s *Service) status(machine Machine) Status {
	if machine.IsLocal() {
		machine.LastSeenAt = s.now()
		return Status{Machine: machine, Online: true}
	}
	return Status{Machine: machine, Online: s.sessions.IsOnline(machine.ID)}
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

// CreateToken émet le jeton d'une nouvelle machine, nommée par l'opérateur.
func (s *Service) CreateToken(ctx context.Context, name string) (string, Token, error) {
	if err := ValidateName(name); err != nil {
		return "", Token{}, err
	}
	return s.issueToken(ctx, name, "")
}

// CreateReenrollToken émet un jeton qui remplace la clé d'une machine
// existante : elle garde son id et son historique.
func (s *Service) CreateReenrollToken(ctx context.Context, machineID string) (string, Token, error) {
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return "", Token{}, err
	}
	if machine.IsLocal() {
		return "", Token{}, ErrLocalMachine
	}
	return s.issueToken(ctx, machine.Name, machine.ID)
}

func (s *Service) issueToken(ctx context.Context, name, machineID string) (string, Token, error) {
	cleartext, token, err := NewToken(name, machineID)
	if err != nil {
		return "", Token{}, fmt.Errorf("generate token: %w", err)
	}
	now := s.now()
	token.CreatedAt = now
	token.ExpiresAt = now.Add(TokenLifetime)
	if err := s.store.InsertToken(ctx, token); err != nil {
		return "", Token{}, fmt.Errorf("insert token: %w", err)
	}
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
	s.changed("")
	return nil
}

// Remove retire une machine et coupe son flux ; la machine openCloud reste.
func (s *Service) Remove(ctx context.Context, id string) error {
	machine, err := s.store.GetMachine(ctx, id)
	if err != nil {
		return err
	}
	if machine.IsLocal() {
		return ErrLocalMachine
	}
	if err := s.store.DeleteMachine(ctx, id); err != nil {
		return err
	}
	s.sessions.Close(id)
	s.logger.Info("machine removed", "machine_id", id, "name", machine.Name)
	s.changed(id)
	return nil
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
	now := s.now()
	candidate := Machine{
		ID:           request.MachineID,
		Kind:         KindRemote,
		PublicKey:    request.PublicKey,
		Hostname:     request.Hostname,
		Address:      request.Address,
		OS:           request.OS,
		Arch:         request.Arch,
		AgentVersion: request.AgentVersion,
		EnrolledAt:   now,
		CreatedAt:    now,
	}
	machine, err := s.store.Enroll(ctx, HashToken(request.Token), candidate, now)
	if err != nil {
		return Machine{}, err
	}
	// Un ré-enrôlement remplace la clé : l'ancien flux ne vaut plus rien.
	s.sessions.Close(machine.ID)
	s.logger.Info("machine enrolled", "machine_id", machine.ID, "name", machine.Name)
	s.changed(machine.ID)
	return machine, nil
}

// Challenge émet le défi qu'un agent devra signer pour ouvrir son flux.
func (s *Service) Challenge(ctx context.Context, machineID string) ([]byte, error) {
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		return nil, err
	}
	if machine.IsLocal() {
		return nil, ErrNotFound
	}
	return s.nonces.issue(machineID, s.now())
}

// Ce que l'agent présente pour ouvrir son flux.
type Proof struct {
	MachineID string
	Nonce     []byte
	Timestamp int64
	Signature []byte
}

// Authenticate vérifie la preuve : défi émis pour cette machine, horloge dans
// la tolérance, signature de la clé enrôlée.
func (s *Service) Authenticate(ctx context.Context, proof Proof) (Machine, error) {
	machine, err := s.store.GetMachine(ctx, proof.MachineID)
	if err != nil {
		return Machine{}, err
	}
	if machine.IsLocal() {
		return Machine{}, ErrNotFound
	}
	now := s.now()
	if !s.nonces.consume(proof.Nonce, proof.MachineID, now) {
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
	if !Verify(machine.PublicKey, payload, proof.Signature) {
		return Machine{}, ErrBadSignature
	}
	return machine, nil
}

// Connect ouvre la session d'une machine authentifiée et note sa venue.
func (s *Service) Connect(ctx context.Context, machineID, address, agentVersion string) (*Session, error) {
	now := s.now()
	if err := s.store.RecordConnection(ctx, machineID, address, agentVersion, now); err != nil {
		return nil, fmt.Errorf("record connection: %w", err)
	}
	session, err := s.sessions.Open(machineID, now)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	s.logger.Info("machine connected", "machine_id", machineID, "address", address)
	s.changed(machineID)
	return session, nil
}

// Disconnect libère la session quand son flux se termine.
func (s *Service) Disconnect(session *Session) {
	s.sessions.Release(session)
	s.logger.Info("machine disconnected", "machine_id", session.MachineID)
	s.changed(session.MachineID)
}

// Signal note qu'une machine dont le flux est ouvert vient de donner signe
// de vie, et rend son identifiant : ce que le signal porte avec lui, les
// échantillons de ressources, s'écrit à ce nom.
func (s *Service) Signal(ctx context.Context, sessionToken string) (string, error) {
	session, ok := s.sessions.Lookup(sessionToken)
	if !ok {
		return "", ErrNotConnected
	}
	if err := s.store.TouchMachine(ctx, session.MachineID, s.now()); err != nil {
		return "", err
	}
	s.changed(session.MachineID)
	return session.MachineID, nil
}
