package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Un corps de requête ne dépasse jamais ceci, quelle que soit la route ;
// l'API et les pings ont leurs bornes plus serrées en dessous.
const (
	maxRequestBody     = 1 << 20
	requestIDBytes     = 8
	requestIDHeader    = "X-Request-ID"
	panicResponseTitle = "internal server error"
	pingPrefix         = "/ping/"
	redacted           = "[redacted]"
)

// La chaîne, de l'extérieur vers l'intérieur : panique, identifiant,
// journal, limite de corps. L'identifiant précède le journal pour que la
// ligne le porte ; la panique enveloppe tout, y compris le journal.
func (s *Server) chain(next http.Handler) http.Handler {
	return s.recoverPanic(requestID(s.logRequest(limitBody(next))))
}

// statusWriter garde le statut pour le journal. Flush et Unwrap relaient
// vers l'écrivain d'origine : sans eux, un flux SSE derrière ce middleware
// ne partirait jamais.
type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// recoverPanic transforme une panique en 500 journalisé, JSON sous /api,
// texte ailleurs. Si la réponse est déjà partie, il ne reste que le journal.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			s.logger.Error("panic", "error", recovered, "method", r.Method, "path", loggedPath(r), "request_id", w.Header().Get(requestIDHeader))
			if writer.written {
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				s.writeAPIError(writer, http.StatusInternalServerError, codeInternal)
				return
			}
			http.Error(writer, panicResponseTitle, http.StatusInternalServerError)
		}()
		next.ServeHTTP(writer, r)
	})
}

// requestID tire un identifiant court par requête et le rend dans la
// réponse : l'opérateur peut le citer, le journal le porte. Jamais lu
// depuis le client : personne ne choisit ce qui entre dans nos journaux.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(requestIDHeader, newRequestID())
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	raw := make([]byte, requestIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(raw)
}

// logRequest écrit une ligne par requête finie : Debug d'ordinaire, Warn
// dès un 500. Un flux SSE ne fait sa ligne qu'à sa fermeture, avec sa durée.
func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer, ok := w.(*statusWriter)
		if !ok {
			writer = &statusWriter{ResponseWriter: w}
		}
		started := time.Now()
		next.ServeHTTP(writer, r)
		level := slog.LevelDebug
		if writer.status >= http.StatusInternalServerError {
			level = slog.LevelWarn
		}
		s.logger.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", loggedPath(r),
			"status", writer.status,
			"duration_ms", time.Since(started).Milliseconds(),
			"request_id", w.Header().Get(requestIDHeader),
		)
	})
}

// loggedPath tait ce qui est un secret dans l'adresse : le jeton d'un
// ping, qui suffit à faire passer une tâche pour faite, et la requête de
// l'autorisation OAuth, qui porte state et le défi PKCE. Le reste du
// chemin garde ce qu'il faut pour suivre une requête.
func loggedPath(r *http.Request) string {
	path := r.URL.Path
	if rest, ok := strings.CutPrefix(path, pingPrefix); ok {
		_, after, hasMore := strings.Cut(rest, "/")
		if hasMore {
			return pingPrefix + redacted + "/" + after
		}
		return pingPrefix + redacted
	}
	if path == mcpAuthorizePath && r.URL.RawQuery != "" {
		return path + "?" + redacted
	}
	return path
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}
