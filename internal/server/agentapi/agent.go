package agentapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
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
	if !h.speaksProtocol(w, r) {
		return
	}
	// Le même seau que l'ouverture du flux : un enrôlement légitime est rare,
	// un flot d'enrôlements coûte une transaction chacun.
	if !h.sourceLimit.Allow(h.clientAddress(r)) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	var request agentwire.EnrollRequest
	if !h.readJSON(w, r, &request) {
		return
	}
	// La clé que l'agent épinglera, lue avant de consommer le jeton : sans
	// elle, l'agent ne pourrait vérifier aucun ordre.
	serverKey, err := h.serverKey()
	if err != nil {
		h.logger.Error("enroll without a server key", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, "server_key_missing")
		return
	}
	publicKey, err := base64.StdEncoding.DecodeString(request.PublicKey)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "bad_key")
		return
	}
	// L'agent agit sous l'identifiant qu'il présente : le jeton, consommé
	// dans la même transaction, dit s'il en avait le droit.
	ctx := audit.WithActor(r.Context(), audit.Agent(request.MachineID))
	enrolled, err := h.machines.Enroll(ctx, machine.Enrollment{
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
	h.writeJSON(w, http.StatusCreated, agentwire.EnrollResponse{
		MachineID: enrolled.ID, Name: enrolled.Name, AgentID: enrolled.Agent.ID,
		ServerKey: base64.StdEncoding.EncodeToString(serverKey),
	})
}

func (h *Handler) serverKey() ([]byte, error) {
	if h.actions == nil {
		return nil, errNoActions
	}
	public, _, err := h.actions.ServerKey()
	return public, err
}

// errNoActions : le handler est monté sans composant action (un banc de
// test qui ne s'en sert pas).
var errNoActions = errors.New("no action component")

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
	case errors.Is(err, machine.ErrLocalAgentActive):
		h.writeError(w, http.StatusConflict, agentwire.CodeLocalAgentActive)
	default:
		h.logger.Error("enroll", "path", r.URL.Path, "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
	}
}

// speaksProtocol refuse un agent d'une autre version du protocole, en le
// disant : 426 protocol_unsupported. Un agent du protocole 1 ne pose pas
// l'en-tête.
func (h *Handler) speaksProtocol(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(agentwire.HeaderProtocol) == agentwire.ProtocolVersion {
		return true
	}
	h.writeError(w, http.StatusUpgradeRequired, agentwire.CodeProtocolUnsupported)
	return false
}

// unknown répond à un chemin d'agent que ce serveur ne sert pas : 426 pour
// un agent d'une autre version (le défi du protocole 1 tombe ici), 404
// sinon.
func (h *Handler) unknown(w http.ResponseWriter, r *http.Request) {
	if !h.speaksProtocol(w, r) {
		return
	}
	h.writeError(w, http.StatusNotFound, "not_found")
}

// Le flux d'une machine : la preuve est vérifiée, la session ouverte, puis
// la réponse reste ouverte en SSE. Le premier événement donne le jeton de
// session ; ensuite un ping tient la connexion, et « closed » dit à l'agent
// pourquoi le serveur raccroche.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	if !h.speaksProtocol(w, r) {
		return
	}
	if !h.sourceLimit.Allow(h.clientAddress(r)) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
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
	// L'empreinte de la clé que l'agent a épinglée : la fiche montre un
	// agent qui refuserait les ordres de ce serveur.
	if err := h.machines.NoteServerKey(r.Context(), authenticated.Agent.ID, r.Header.Get(agentwire.HeaderServerKey)); err != nil {
		h.logger.Warn("note server key", "machine_id", authenticated.ID, "error", err)
	}
	// Les droits que l'agent lit dans son fichier : stockés tels qu'il les
	// dit, comparés à ce que son jeton demandait ; l'agent les applique, le
	// serveur s'en sert pour ne pas envoyer un ordre qu'il sait refusé.
	session, err := h.machines.Connect(r.Context(), authenticated, h.clientAddress(r),
		r.Header.Get(agentwire.HeaderAgentVersion), r.Header.Get(agentwire.HeaderAgentRights))
	if err != nil {
		h.logger.Error("connect", "machine_id", authenticated.ID, "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	defer h.machines.Disconnect(session)
	// Le flux qui s'ouvre repart avec le jeu de sondes de sa machine et
	// les images à ne pas interroger : les commandes attendent dans la
	// file que la boucle ci-dessous va lire.
	h.probes.Assign(r.Context(), authenticated.ID)
	h.updates.Assign(r.Context(), authenticated.ID)
	// Pendant une rotation de la clé du serveur, un agent resté sur la
	// précédente reçoit la neuve, par un ordre que la précédente signe. C'est
	// serve qui le décide : une règle, au journal.
	if h.actions != nil {
		rekeyCtx := audit.WithActor(r.Context(), audit.Rule("server_key_rotation"))
		if err := h.actions.Rekey(rekeyCtx, authenticated.ID); err != nil {
			h.logger.Warn("rekey agent", "machine_id", authenticated.ID, "error", err)
		}
	}

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
		case event := <-session.Events():
			if err := writeAgentEvent(w, event); err != nil {
				return
			}
		}
	}
}

// writeAgentEvent pousse un événement à l'agent, son contenu découpé en
// lignes « data: » d'au plus agentwire.EventDataLine octets, que l'agent
// recolle sans séparateur. Un contenu au-delà de MaxEventBytes ne part pas :
// l'agent couperait le flux.
func writeAgentEvent(w http.ResponseWriter, event machine.StreamEvent) error {
	if len(event.Data) > agentwire.MaxEventBytes {
		return fmt.Errorf("event %s: %d bytes, more than an agent reads", event.Name, len(event.Data))
	}
	var text strings.Builder
	text.WriteString("event: " + event.Name + "\n")
	for data := event.Data; ; {
		line := data[:min(len(data), agentwire.EventDataLine)]
		text.WriteString("data: ")
		text.Write(line)
		text.WriteString("\n")
		data = data[len(line):]
		if len(data) == 0 {
			break
		}
	}
	text.WriteString("\n")
	if _, err := io.WriteString(w, text.String()); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func (h *Handler) writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, machine.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "machine_not_found")
	case errors.Is(err, machine.ErrClockSkew):
		h.writeError(w, http.StatusUnauthorized, "clock_skew")
	// Une preuve déjà servie n'est pas un refus d'identité : l'agent en signe
	// une autre. Seul bad_proof l'arrête.
	case errors.Is(err, machine.ErrReplayed):
		h.writeError(w, http.StatusUnauthorized, agentwire.CodeReplayed)
	case errors.Is(err, machine.ErrBadSignature), errors.Is(err, machine.ErrBadID), errors.Is(err, machine.ErrBadProof):
		h.writeError(w, http.StatusUnauthorized, "bad_proof")
	// L'agent a prouvé sa clé, mais l'opérateur l'a révoqué : il s'arrête,
	// comme pour bad_proof. Un agent plus vieux ne connaît pas ce code et
	// réessaie, avec son délai croissant, sans jamais être admis.
	case errors.Is(err, machine.ErrAgentRevoked):
		h.writeError(w, http.StatusUnauthorized, agentwire.CodeRevoked)
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
	if !h.signalLimit.Allow(sessionMachine) {
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
	// Rien d'un signal n'est journalisé aujourd'hui (des observations) ; ce
	// qu'il ferait écrire un jour le serait sous sa machine.
	ctx := audit.WithActor(r.Context(), audit.Agent(machineID))
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
	if request.Actions != nil {
		keep(agentwire.SectionActions, h.recordActions(ctx, machineID, *request.Actions), action.ErrReportInvalid)
	}
	return rejected, failed
}

// recordActions écrit ce que l'agent a fait des ordres ; sans composant
// action, la section est refusée.
func (h *Handler) recordActions(ctx context.Context, machineID string, report agentwire.ActionReport) error {
	if h.actions == nil {
		return action.ErrReportInvalid
	}
	return h.actions.Record(ctx, machineID, report)
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
	if err != nil || len(nonce) != machine.NonceSize {
		return machine.Proof{}, fmt.Errorf("decode nonce: %w", machine.ErrBadProof)
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

// Un fichier de colis, pour la machine dont la session le demande : un colis
// d'une autre machine, échu ou inconnu répond 404 sans distinguer ces cas.
func (h *Handler) parcel(w http.ResponseWriter, r *http.Request) {
	if !h.speaksProtocol(w, r) {
		return
	}
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
	if !h.parcelLimit.Allow(machineID) {
		w.Header().Set("Retry-After", httpx.RetryAfterSeconds)
		h.writeError(w, http.StatusTooManyRequests, "too_many_requests")
		return
	}
	if h.actions == nil {
		h.writeError(w, http.StatusNotFound, "parcel_not_found")
		return
	}
	file, size, err := h.actions.OpenParcel(machineID, r.PathValue("parcel"), r.PathValue("name"))
	if errors.Is(err, action.ErrParcelNotFound) {
		h.writeError(w, http.StatusNotFound, "parcel_not_found")
		return
	}
	if err != nil {
		h.logger.Error("open parcel", "machine_id", machineID, "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, file); err != nil {
		h.logger.Warn("send parcel", "machine_id", machineID, "error", err)
	}
}
