package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/store"
)

// Un commentaire régulier tient la connexion ouverte à travers les proxys et
// dit au navigateur que le flux vit toujours.
const streamHeartbeat = 15 * time.Second

// streamAction rejoue les lignes déjà stockées puis diffuse les suivantes.
// L'abonnement est posé avant la relecture : rien ne se perd entre les deux, et
// le seq écarte les doublons.
func (s *Server) streamAction(w http.ResponseWriter, r *http.Request, _ store.Account) {
	if !s.actionsReady() {
		http.NotFound(w, r)
		return
	}
	actionID := r.PathValue("id")

	events, unsubscribe := s.actions.Subscribe(actionID)
	defer unsubscribe()

	action, err := s.actions.Action(r.Context(), actionID)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, messageActionUnknown, http.StatusNotFound)
		return
	}
	if err != nil {
		s.serverError(w, "read action", err)
		return
	}

	lastSeq := resumeSeq(r)
	lines, err := s.actions.Lines(r.Context(), actionID, lastSeq)
	if err != nil {
		s.serverError(w, "read action lines", err)
		return
	}

	stream := s.openStream(w)
	for _, line := range lines {
		stream.line(line.Seq, line.At, line.Text)
		lastSeq = line.Seq
	}
	if stream.err != nil {
		return
	}
	// L'action était déjà conclue : on le dit et on ferme, il n'y a rien à
	// attendre.
	if concluded(action.State) {
		stream.done(action.State, action.Result)
		return
	}

	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			stream.heartbeat()
		case event, open := <-events:
			if !open {
				return
			}
			if event.Done {
				stream.done(event.State, event.Result)
				return
			}
			if event.Seq <= lastSeq {
				continue
			}
			stream.line(event.Seq, event.At, event.Text)
			lastSeq = event.Seq
		}
		if stream.err != nil {
			return
		}
	}
}

// resumeSeq : le navigateur renvoie Last-Event-ID après une coupure ; la page
// pose ?after= au premier appel avec ce qu'elle affiche déjà.
func resumeSeq(r *http.Request) int64 {
	if header := r.Header.Get("Last-Event-ID"); header != "" {
		if seq, err := strconv.ParseInt(header, 10, 64); err == nil && seq > 0 {
			return seq
		}
	}
	seq, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || seq < 0 {
		return 0
	}
	return seq
}

func streamPath(actionID string, afterSeq int64) string {
	return fmt.Sprintf("/actions/%s/stream?after=%d", actionID, afterSeq)
}

// eventStream écrit le flux SSE. Les données sont du JSON : le texte d'une
// ligne y est échappé, sans avoir à se demander ce qu'un script a écrit.
type eventStream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	logger     *slog.Logger
	err        error
}

func (s *Server) openStream(w http.ResponseWriter) *eventStream {
	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("X-Accel-Buffering", "no")

	stream := &eventStream{writer: w, controller: http.NewResponseController(w), logger: s.logger}
	// Le serveur borne l'écriture à 30 s ; un direct dure ce que dure
	// l'action, donc l'échéance est levée sur cette réponse seulement.
	if err := stream.controller.SetWriteDeadline(time.Time{}); err != nil {
		s.logger.Warn("lift write deadline for stream", "error", err)
	}
	w.WriteHeader(http.StatusOK)
	stream.flush()
	return stream
}

func (s *eventStream) line(seq int64, at time.Time, text string) {
	payload := struct {
		Seq  int64  `json:"seq"`
		At   string `json:"at"`
		Text string `json:"text"`
	}{Seq: seq, At: at.UTC().Format(time.RFC3339), Text: text}
	s.send(fmt.Sprintf("id: %d\n", seq), "line", payload)
}

func (s *eventStream) done(state store.ActionState, result string) {
	payload := struct {
		State      string `json:"state"`
		StateLabel string `json:"state_label"`
		Result     string `json:"result"`
	}{State: string(state), StateLabel: actionStateLabel(state), Result: result}
	s.send("", "done", payload)
}

func (s *eventStream) heartbeat() {
	s.write(": battement\n\n")
	s.flush()
}

func (s *eventStream) send(idLine, name string, payload any) {
	if s.err != nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.err = err
		return
	}
	s.write(idLine + "event: " + name + "\ndata: " + string(data) + "\n\n")
	s.flush()
}

func (s *eventStream) write(text string) {
	if s.err != nil {
		return
	}
	if _, err := s.writer.Write([]byte(text)); err != nil {
		// Le navigateur est parti : rien à réparer, on s'arrête.
		s.err = err
		s.logger.Debug("write stream event", "error", err)
	}
}

func (s *eventStream) flush() {
	if s.err != nil {
		return
	}
	if err := s.controller.Flush(); err != nil {
		s.err = err
	}
}
