package agentapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
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

func (h *Handler) enroll(w http.ResponseWriter, r *http.Request) {
	// Le même seau que le défi : un enrôlement légitime est rare, un flot
	// d'enrôlements coûte une transaction chacun.
	if !h.challengeLimits.allow(h.clientAddress(r)) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	var request agentwire.EnrollRequest
	if !h.readJSON(w, r, &request) {
		return
	}
	publicKey, err := base64.StdEncoding.DecodeString(request.PublicKey)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "bad_key")
		return
	}
	enrolled, err := h.machines.Enroll(r.Context(), machine.Enrollment{
		MachineID:    request.MachineID,
		PublicKey:    publicKey,
		Token:        request.Token,
		Hostname:     request.Hostname,
		Address:      h.clientAddress(r),
		OS:           request.OS,
		Arch:         request.Arch,
		AgentVersion: request.AgentVersion,

		HostFingerprint: request.HostFingerprint,
	})
	if err != nil {
		h.writeEnrollError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, agentwire.EnrollResponse{MachineID: enrolled.ID, Name: enrolled.Name})
}

func (h *Handler) writeEnrollError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, machine.ErrBadID), errors.Is(err, machine.ErrBadKey):
		h.writeError(w, http.StatusBadRequest, "bad_request")
	case errors.Is(err, machine.ErrTokenNotFound):
		h.writeError(w, http.StatusNotFound, "token_not_found")
	case errors.Is(err, machine.ErrTokenConsumed):
		h.writeError(w, http.StatusConflict, "token_consumed")
	case errors.Is(err, machine.ErrTokenExpired):
		h.writeError(w, http.StatusConflict, "token_expired")
	case errors.Is(err, machine.ErrNameTaken):
		h.writeError(w, http.StatusConflict, "name_taken")
	case errors.Is(err, machine.ErrIsOpenCloud):
		h.writeError(w, http.StatusConflict, brand.AgentErrorIsHost)
	default:
		h.logger.Error("enroll", "path", r.URL.Path, "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
	}
}

func (h *Handler) challenge(w http.ResponseWriter, r *http.Request) {
	if !h.challengeLimits.allow(h.clientAddress(r)) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	var request agentwire.ChallengeRequest
	if !h.readJSON(w, r, &request) {
		return
	}
	nonce, err := h.machines.Challenge(r.Context(), request.MachineID)
	if errors.Is(err, machine.ErrNotFound) {
		h.writeError(w, http.StatusNotFound, "machine_not_found")
		return
	}
	if err != nil {
		h.logger.Error("challenge", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	h.writeJSON(w, http.StatusOK, agentwire.ChallengeResponse{Nonce: base64.StdEncoding.EncodeToString(nonce)})
}

// Le flux d'une machine : la preuve est vérifiée, la session ouverte, puis
// la réponse reste ouverte en SSE. Le premier événement donne le jeton de
// session ; ensuite un ping tient la connexion, et « closed » dit à l'agent
// pourquoi le serveur raccroche.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	proof, err := proofFromHeaders(r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "bad_proof")
		return
	}
	authenticated, err := h.machines.Authenticate(r.Context(), proof)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}
	// Le système que l'agent redit : s'il ne s'écrit pas, le flux s'ouvre
	// quand même, sur celui de l'enrôlement.
	if err := h.machines.NoteSystem(r.Context(), authenticated.ID, r.Header.Get(agentwire.HeaderAgentOS)); err != nil {
		h.logger.Warn("note system", "machine_id", authenticated.ID, "error", err)
	}
	session, err := h.machines.Connect(r.Context(), authenticated.ID, h.clientAddress(r), r.Header.Get(agentwire.HeaderAgentVersion))
	if err != nil {
		h.logger.Error("connect", "machine_id", authenticated.ID, "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	defer h.machines.Disconnect(session)
	h.machines.WarnIfOpenCloudHost(authenticated, r.Header.Get(agentwire.HeaderHostFingerprint))
	// Le flux qui s'ouvre repart avec le jeu de sondes de sa machine et
	// les images à ne pas interroger : les commandes attendent dans la
	// file que la boucle ci-dessous va lire.
	h.probes.Assign(r.Context(), authenticated.ID)
	h.updates.Assign(r.Context(), authenticated.ID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := httpx.WriteEvent(w, "session", session.Token); err != nil {
		return
	}
	ticker := time.NewTicker(streamPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-session.Done():
			_ = httpx.WriteEvent(w, "closed", "session_closed")
			return
		case <-ticker.C:
			if err := httpx.WriteEvent(w, "ping", ""); err != nil {
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
	return httpx.WriteEvent(w, command.Name, string(payload))
}

func (h *Handler) writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, machine.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "machine_not_found")
	case errors.Is(err, machine.ErrClockSkew):
		h.writeError(w, http.StatusUnauthorized, "clock_skew")
	// Un défi expiré, déjà utilisé ou perdu au redémarrage n'est pas un refus
	// d'identité : l'agent en redemande un. Seul bad_proof l'arrête.
	case errors.Is(err, machine.ErrBadNonce):
		h.writeError(w, http.StatusUnauthorized, "challenge_expired")
	case errors.Is(err, machine.ErrBadSignature), errors.Is(err, machine.ErrBadID):
		h.writeError(w, http.StatusUnauthorized, "bad_proof")
	default:
		h.logger.Error("authenticate", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
	}
}

// Un signal de vie, authentifié par le jeton de la session ouverte. Son
// corps, facultatif, porte les lectures de ressources faites depuis le
// précédent, puis les sections services, sondes et images. Chaque section
// s'écrit seule : une section hors de ce qu'un agent peut mesurer est
// refusée sans emporter les autres, le signal de vie compte quand même.
func (h *Handler) signal(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	// La session d'abord, sans rien écrire, puis le débit de sa machine : un
	// corps d'un mégaoctet ne se lit pas pour être refusé ensuite, et le seau
	// est celui de la machine, pas de l'adresse que des agents se partagent.
	sessionMachine, err := h.machines.SessionMachine(token)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	if !h.signalLimits.allow(sessionMachine) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	var request agentwire.SignalRequest
	if r.ContentLength != 0 {
		body := http.MaxBytesReader(w, r.Body, maxSignalBody)
		if err := json.NewDecoder(body).Decode(&request); err != nil {
			h.writeError(w, http.StatusBadRequest, "bad_json")
			return
		}
	}
	machineID, err := h.machines.Signal(r.Context(), token)
	if errors.Is(err, machine.ErrNotConnected) {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	if err != nil {
		h.logger.Error("signal", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	rejected, failed := h.recordSignal(r, machineID, request)
	switch {
	case failed:
		// L'agent remet tout dans son tampon : une lecture rejouée est
		// ignorée, un rapport rejoué ne crée pas de transition sans changement.
		h.writeError(w, http.StatusInternalServerError, "internal")
	case len(rejected) == 0:
		w.WriteHeader(http.StatusNoContent)
	case r.Header.Get(agentwire.HeaderSignalSections) == "":
		// Un agent plus vieux ne connaît que le refus global : il jette son
		// lot, ce qui a été accepté est déjà écrit.
		h.writeError(w, http.StatusBadRequest, firstRejected(rejected))
	default:
		h.writeJSON(w, http.StatusOK, agentwire.SignalResponse{Rejected: rejected})
	}
}

// recordSignal écrit chaque section indépendamment. Il rend les sections
// refusées avec leur clé, et dit si une écriture a échoué pour une raison
// qui n'est pas un refus.
func (h *Handler) recordSignal(r *http.Request, machineID string, request agentwire.SignalRequest) (map[string]string, bool) {
	ctx := r.Context()
	rejected := map[string]string{}
	failed := false
	keep := func(section string, err error, refusals ...error) {
		if err == nil {
			return
		}
		for _, refusal := range refusals {
			if errors.Is(err, refusal) {
				h.logger.Warn("signal section refused", "machine_id", machineID, "section", section, "error", err)
				rejected[section] = agentwire.SectionRefusal(section)
				return
			}
		}
		h.logger.Error("record "+section, "machine_id", machineID, "error", err)
		failed = true
	}
	keep(agentwire.SectionReadings, h.resources.Record(ctx, machineID, request.Readings), resource.ErrReadingInvalid, resource.ErrTooManyReadings)
	// Un agent plus vieux n'envoie pas toutes les sections : rien à écrire.
	if request.Services != nil {
		keep(agentwire.SectionServices, h.containers.Record(ctx, machineID, *request.Services), container.ErrReportInvalid)
	}
	if request.Probes != nil {
		keep(agentwire.SectionProbes, h.probes.Record(ctx, machineID, *request.Probes), probe.ErrReportInvalid)
	}
	if request.Images != nil {
		keep(agentwire.SectionImages, h.updates.Record(ctx, machineID, *request.Images), update.ErrReportInvalid)
	}
	return rejected, failed
}

// firstRejected rend la clé de la première section refusée, dans l'ordre
// où l'ancien serveur les lisait.
func firstRejected(rejected map[string]string) string {
	for _, section := range agentwire.SignalSections() {
		if code, ok := rejected[section]; ok {
			return code
		}
	}
	return "bad_json"
}

// Un lot de journal livré par l'agent pour une requête que le serveur lui
// a poussée. Une requête que personne n'attend plus, ou poussée à une autre
// machine, répond 404 : l'agent arrête de suivre.
func (h *Handler) logs(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	machineID, err := h.machines.SessionMachine(token)
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	var batch container.LogBatch
	body := http.MaxBytesReader(w, r.Body, maxLogsBody)
	if err := json.NewDecoder(body).Decode(&batch); err != nil {
		h.writeError(w, http.StatusBadRequest, "bad_json")
		return
	}
	switch err := h.containers.DeliverLogs(r.Context(), machineID, r.PathValue("request"), batch); {
	case errors.Is(err, container.ErrLogBatchTooLarge):
		h.writeError(w, http.StatusRequestEntityTooLarge, "batch_too_large")
	case err != nil:
		h.writeError(w, http.StatusNotFound, "request_unknown")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func proofFromHeaders(r *http.Request) (machine.Proof, error) {
	nonce, err := base64.StdEncoding.DecodeString(r.Header.Get(agentwire.HeaderNonce))
	if err != nil {
		return machine.Proof{}, fmt.Errorf("decode nonce: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get(agentwire.HeaderSignature))
	if err != nil {
		return machine.Proof{}, fmt.Errorf("decode signature: %w", err)
	}
	timestamp, err := strconv.ParseInt(r.Header.Get(agentwire.HeaderTimestamp), 10, 64)
	if err != nil {
		return machine.Proof{}, fmt.Errorf("parse timestamp: %w", err)
	}
	return machine.Proof{
		MachineID: r.Header.Get(agentwire.HeaderMachine),
		Nonce:     nonce,
		Timestamp: timestamp,
		Signature: signature,
	}, nil
}

func (h *Handler) readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	body := http.MaxBytesReader(w, r.Body, maxAgentBody)
	if err := json.NewDecoder(body).Decode(into); err != nil {
		h.writeError(w, http.StatusBadRequest, "bad_json")
		return false
	}
	return true
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	if err := httpx.WriteJSON(w, status, payload); err != nil {
		h.logger.Warn("write json", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code string) {
	h.writeJSON(w, status, agentError{Error: code})
}
