package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
)

const (
	streamPingInterval = 15 * time.Second
	maxAgentBody       = 16 << 10
	// Un lot de rattrapage : 120 lectures d'environ 250 octets, plus une
	// centaine par volume, jusqu'à 32 volumes.
	maxSignalBody = 512 << 10
)

type agentError struct {
	Error string `json:"error"`
}

func (s *Server) agentEnroll(w http.ResponseWriter, r *http.Request) {
	var request machine.EnrollRequest
	if !s.readJSON(w, r, &request) {
		return
	}
	publicKey, err := base64.StdEncoding.DecodeString(request.PublicKey)
	if err != nil {
		s.writeAgentError(w, http.StatusBadRequest, "bad_key")
		return
	}
	enrolled, err := s.machines.Enroll(r.Context(), machine.Enrollment{
		MachineID:    request.MachineID,
		PublicKey:    publicKey,
		Token:        request.Token,
		Hostname:     request.Hostname,
		Address:      s.clientAddress(r),
		OS:           request.OS,
		Arch:         request.Arch,
		AgentVersion: request.AgentVersion,
	})
	if err != nil {
		s.writeEnrollError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, machine.EnrollResponse{MachineID: enrolled.ID, Name: enrolled.Name})
}

func (s *Server) writeEnrollError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, machine.ErrBadID), errors.Is(err, machine.ErrBadKey):
		s.writeAgentError(w, http.StatusBadRequest, "bad_request")
	case errors.Is(err, machine.ErrTokenNotFound):
		s.writeAgentError(w, http.StatusNotFound, "token_not_found")
	case errors.Is(err, machine.ErrTokenConsumed):
		s.writeAgentError(w, http.StatusConflict, "token_consumed")
	case errors.Is(err, machine.ErrTokenExpired):
		s.writeAgentError(w, http.StatusConflict, "token_expired")
	case errors.Is(err, machine.ErrNameTaken):
		s.writeAgentError(w, http.StatusConflict, "name_taken")
	default:
		s.logger.Error("enroll", "path", r.URL.Path, "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
	}
}

func (s *Server) agentChallenge(w http.ResponseWriter, r *http.Request) {
	var request machine.ChallengeRequest
	if !s.readJSON(w, r, &request) {
		return
	}
	nonce, err := s.machines.Challenge(r.Context(), request.MachineID)
	if errors.Is(err, machine.ErrNotFound) {
		s.writeAgentError(w, http.StatusNotFound, "machine_not_found")
		return
	}
	if err != nil {
		s.logger.Error("challenge", "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
		return
	}
	s.writeJSON(w, http.StatusOK, machine.ChallengeResponse{Nonce: base64.StdEncoding.EncodeToString(nonce)})
}

// Le flux d'une machine : la preuve est vérifiée, la session ouverte, puis
// la réponse reste ouverte en SSE. Le premier événement donne le jeton de
// session ; ensuite un ping tient la connexion, et « closed » dit à l'agent
// pourquoi le serveur raccroche.
func (s *Server) agentStream(w http.ResponseWriter, r *http.Request) {
	proof, err := proofFromHeaders(r)
	if err != nil {
		s.writeAgentError(w, http.StatusBadRequest, "bad_proof")
		return
	}
	authenticated, err := s.machines.Authenticate(r.Context(), proof)
	if err != nil {
		s.writeAuthError(w, err)
		return
	}
	session, err := s.machines.Connect(r.Context(), authenticated.ID, s.clientAddress(r), r.Header.Get(machine.HeaderAgentVersion))
	if err != nil {
		s.logger.Error("connect", "machine_id", authenticated.ID, "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
		return
	}
	defer s.machines.Disconnect(session)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := writeEvent(w, "session", session.Token); err != nil {
		return
	}
	ticker := time.NewTicker(streamPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.Done():
			_ = writeEvent(w, "closed", "session_closed")
			return
		case <-ticker.C:
			if err := writeEvent(w, "ping", ""); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, machine.ErrNotFound):
		s.writeAgentError(w, http.StatusNotFound, "machine_not_found")
	case errors.Is(err, machine.ErrClockSkew):
		s.writeAgentError(w, http.StatusUnauthorized, "clock_skew")
	case errors.Is(err, machine.ErrBadNonce), errors.Is(err, machine.ErrBadSignature), errors.Is(err, machine.ErrBadID):
		s.writeAgentError(w, http.StatusUnauthorized, "bad_proof")
	default:
		s.logger.Error("authenticate", "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
	}
}

// Un signal de vie, authentifié par le jeton de la session ouverte. Son
// corps, facultatif, porte les lectures de ressources faites depuis le
// précédent ; un lot hors de ce qu'un agent peut mesurer est refusé en
// bloc, le signal de vie compte quand même.
func (s *Server) agentSignal(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		s.writeAgentError(w, http.StatusUnauthorized, "no_session")
		return
	}
	var request resource.SignalRequest
	if r.ContentLength != 0 {
		body := http.MaxBytesReader(w, r.Body, maxSignalBody)
		if err := json.NewDecoder(body).Decode(&request); err != nil {
			s.writeAgentError(w, http.StatusBadRequest, "bad_json")
			return
		}
	}
	machineID, err := s.machines.Signal(r.Context(), token)
	if errors.Is(err, machine.ErrNotConnected) {
		s.writeAgentError(w, http.StatusUnauthorized, "no_session")
		return
	}
	if err != nil {
		s.logger.Error("signal", "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
		return
	}
	err = s.resources.Record(r.Context(), machineID, request.Readings)
	switch {
	case errors.Is(err, resource.ErrReadingInvalid), errors.Is(err, resource.ErrTooManyReadings):
		s.writeAgentError(w, http.StatusBadRequest, "bad_readings")
	case err != nil:
		s.logger.Error("record readings", "machine_id", machineID, "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func proofFromHeaders(r *http.Request) (machine.Proof, error) {
	nonce, err := base64.StdEncoding.DecodeString(r.Header.Get(machine.HeaderNonce))
	if err != nil {
		return machine.Proof{}, fmt.Errorf("decode nonce: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get(machine.HeaderSignature))
	if err != nil {
		return machine.Proof{}, fmt.Errorf("decode signature: %w", err)
	}
	timestamp, err := strconv.ParseInt(r.Header.Get(machine.HeaderTimestamp), 10, 64)
	if err != nil {
		return machine.Proof{}, fmt.Errorf("parse timestamp: %w", err)
	}
	return machine.Proof{
		MachineID: r.Header.Get(machine.HeaderMachine),
		Nonce:     nonce,
		Timestamp: timestamp,
		Signature: signature,
	}, nil
}

// Un événement SSE, poussé tout de suite : le flux ne bufferise pas. Le
// contenu est un jeton ou un mot du serveur, jamais une entrée de l'agent.
func writeEvent(w http.ResponseWriter, name, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil { // #nosec G705 -- text/event-stream, données du serveur.
		return err
	}
	return http.NewResponseController(w).Flush()
}

func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	body := http.MaxBytesReader(w, r.Body, maxAgentBody)
	if err := json.NewDecoder(body).Decode(into); err != nil {
		s.writeAgentError(w, http.StatusBadRequest, "bad_json")
		return false
	}
	return true
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.logger.Warn("write json", "error", err)
	}
}

func (s *Server) writeAgentError(w http.ResponseWriter, status int, code string) {
	s.writeJSON(w, status, agentError{Error: code})
}
