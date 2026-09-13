package machine

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const sessionTokenBytes = 32

// Session est un flux ouvert par l'agent d'une machine. Son jeton, tiré à
// l'ouverture, authentifie les signaux qui suivent sans nouvelle signature.
type Session struct {
	MachineID   string
	Token       string
	ConnectedAt time.Time
	done        chan struct{}
	closeOnce   sync.Once
}

// Done se ferme quand la session est fermée par le serveur : machine retirée,
// ré-enrôlée, ou remplacée par une nouvelle connexion du même agent.
func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Sessions est le registre en mémoire des flux ouverts, un par machine.
type Sessions struct {
	mu        sync.RWMutex
	byMachine map[string]*Session
	byToken   map[string]*Session
}

func NewSessions() *Sessions {
	return &Sessions{
		byMachine: make(map[string]*Session),
		byToken:   make(map[string]*Session),
	}
}

// Open enregistre un flux ; un flux précédent de la même machine est fermé,
// le plus récent gagne.
func (s *Sessions) Open(machineID string, now time.Time) (*Session, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	session := &Session{
		MachineID:   machineID,
		Token:       hex.EncodeToString(raw),
		ConnectedAt: now,
		done:        make(chan struct{}),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.byMachine[machineID]; ok {
		s.forget(previous)
	}
	s.byMachine[machineID] = session
	s.byToken[session.Token] = session
	return session, nil
}

// Release retire une session quand son flux se termine. Une session déjà
// remplacée n'est pas touchée : le registre appartient au flux le plus récent.
func (s *Sessions) Release(session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.byMachine[session.MachineID]; ok && current == session {
		s.forget(session)
	}
}

// Close ferme le flux d'une machine, s'il y en a un.
func (s *Sessions) Close(machineID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session, ok := s.byMachine[machineID]; ok {
		s.forget(session)
	}
}

func (s *Sessions) forget(session *Session) {
	delete(s.byMachine, session.MachineID)
	delete(s.byToken, session.Token)
	session.close()
}

func (s *Sessions) Lookup(token string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.byToken[token]
	return session, ok
}

func (s *Sessions) IsOnline(machineID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byMachine[machineID]
	return ok
}

func (s *Sessions) CountOnline() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byMachine)
}
