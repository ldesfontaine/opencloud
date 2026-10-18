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

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

const (
	streamPingInterval = 15 * time.Second
	maxAgentBody       = 16 << 10
	// Un lot de rattrapage : 120 lectures d'environ 250 octets, plus une
	// centaine par volume, jusqu'à 32 volumes ; la section services, un
	// inventaire de 256 conteneurs d'un kilo-octet au plus ; et la section
	// sondes, 512 essais de deux cents octets.
	maxSignalBody = 1 << 20
	// Un lot de journal : 64 lignes de 64 Kio au plus, en pratique bien
	// moins ; la chaîne de middlewares plafonne déjà à 1 Mio.
	maxLogsBody = 1 << 20
)

type agentError struct {
	Error string `json:"error"`
}

func (s *Server) agentEnroll(w http.ResponseWriter, r *http.Request) {
	// Le même seau que le défi : un enrôlement légitime est rare, un flot
	// d'enrôlements coûte une transaction chacun.
	if !s.agentLimits.allow(s.clientAddress(r)) {
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.writeAgentError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
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

		HostFingerprint: request.HostFingerprint,
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
	case errors.Is(err, machine.ErrIsOpenCloud):
		s.writeAgentError(w, http.StatusConflict, brand.AgentErrorIsHost)
	default:
		s.logger.Error("enroll", "path", r.URL.Path, "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
	}
}

func (s *Server) agentChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.agentLimits.allow(s.clientAddress(r)) {
		w.Header().Set("Retry-After", retryAfterSeconds)
		s.writeAgentError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
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
	// Le système que l'agent redit : s'il ne s'écrit pas, le flux s'ouvre
	// quand même, sur celui de l'enrôlement.
	if err := s.machines.NoteSystem(r.Context(), authenticated.ID, r.Header.Get(machine.HeaderAgentOS)); err != nil {
		s.logger.Warn("note system", "machine_id", authenticated.ID, "error", err)
	}
	session, err := s.machines.Connect(r.Context(), authenticated.ID, s.clientAddress(r), r.Header.Get(machine.HeaderAgentVersion))
	if err != nil {
		s.logger.Error("connect", "machine_id", authenticated.ID, "error", err)
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
		return
	}
	defer s.machines.Disconnect(session)
	s.machines.WarnIfOpenCloudHost(authenticated, r.Header.Get(machine.HeaderHostFingerprint))
	// Le flux qui s'ouvre repart avec le jeu de sondes de sa machine et
	// les images à ne pas interroger : les commandes attendent dans la
	// file que la boucle ci-dessous va lire.
	s.probes.Assign(r.Context(), authenticated.ID)
	s.updates.Assign(r.Context(), authenticated.ID)

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
		case command := <-session.Commands():
			if err := writeCommand(w, command); err != nil {
				return
			}
		}
	}
}

// writeCommand pousse une commande à l'agent : son nom en événement, son
// contenu en JSON sur une ligne, tiré par le serveur.
func writeCommand(w http.ResponseWriter, command machine.Command) error {
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return err
	}
	return writeEvent(w, command.Name, string(payload))
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
// précédent, puis les sections services, sondes et images. Chaque section
// s'écrit seule : une section hors de ce qu'un agent peut mesurer est
// refusée sans emporter les autres, le signal de vie compte quand même.
func (s *Server) agentSignal(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		s.writeAgentError(w, http.StatusUnauthorized, "no_session")
		return
	}
	var request machine.SignalRequest
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
	rejected, failed := s.recordSignal(r, machineID, request)
	switch {
	case failed:
		// L'agent remet tout dans son tampon : une lecture rejouée est
		// ignorée, un rapport rejoué ne crée pas de transition sans changement.
		s.writeAgentError(w, http.StatusInternalServerError, "internal")
	case len(rejected) == 0:
		w.WriteHeader(http.StatusNoContent)
	case r.Header.Get(machine.HeaderSignalSections) == "":
		// Un agent plus vieux ne connaît que le refus global : il jette son
		// lot, ce qui a été accepté est déjà écrit.
		s.writeAgentError(w, http.StatusBadRequest, firstRejected(rejected))
	default:
		s.writeJSON(w, http.StatusOK, machine.SignalResponse{Rejected: rejected})
	}
}

// recordSignal écrit chaque section indépendamment. Il rend les sections
// refusées avec leur clé, et dit si une écriture a échoué pour une raison
// qui n'est pas un refus.
func (s *Server) recordSignal(r *http.Request, machineID string, request machine.SignalRequest) (map[string]string, bool) {
	ctx := r.Context()
	rejected := map[string]string{}
	failed := false
	keep := func(section string, err error, refusals ...error) {
		if err == nil {
			return
		}
		for _, refusal := range refusals {
			if errors.Is(err, refusal) {
				s.logger.Warn("signal section refused", "machine_id", machineID, "section", section, "error", err)
				rejected[section] = machine.SectionRefusal(section)
				return
			}
		}
		s.logger.Error("record "+section, "machine_id", machineID, "error", err)
		failed = true
	}
	keep(machine.SectionReadings, s.resources.Record(ctx, machineID, request.Readings), resource.ErrReadingInvalid, resource.ErrTooManyReadings)
	// Un agent plus vieux n'envoie pas toutes les sections : rien à écrire.
	if request.Services != nil {
		keep(machine.SectionServices, s.services.Record(ctx, machineID, *request.Services), service.ErrReportInvalid)
	}
	if request.Probes != nil {
		keep(machine.SectionProbes, s.probes.Record(ctx, machineID, *request.Probes), probe.ErrReportInvalid)
	}
	if request.Images != nil {
		keep(machine.SectionImages, s.updates.Record(ctx, machineID, *request.Images), update.ErrReportInvalid)
	}
	return rejected, failed
}

// firstRejected rend la clé de la première section refusée, dans l'ordre
// où l'ancien serveur les lisait.
func firstRejected(rejected map[string]string) string {
	for _, section := range machine.SignalSections() {
		if code, ok := rejected[section]; ok {
			return code
		}
	}
	return "bad_json"
}

// Un lot de journal livré par l'agent pour une requête que le serveur lui
// a poussée. Une requête que personne n'attend plus, ou poussée à une autre
// machine, répond 404 : l'agent arrête de suivre.
func (s *Server) agentLogs(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		s.writeAgentError(w, http.StatusUnauthorized, "no_session")
		return
	}
	machineID, err := s.machines.SessionMachine(token)
	if err != nil {
		s.writeAgentError(w, http.StatusUnauthorized, "no_session")
		return
	}
	var batch service.LogBatch
	body := http.MaxBytesReader(w, r.Body, maxLogsBody)
	if err := json.NewDecoder(body).Decode(&batch); err != nil {
		s.writeAgentError(w, http.StatusBadRequest, "bad_json")
		return
	}
	if !s.services.DeliverLogs(r.Context(), machineID, r.PathValue("request"), batch) {
		s.writeAgentError(w, http.StatusNotFound, "request_unknown")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
