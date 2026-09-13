package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	csrfCookieName = "opencloud_csrf"
	csrfFieldName  = "csrf"
	csrfTokenBytes = 32
)

// Jeton anti-CSRF en double soumission : un cookie posé au premier passage,
// recopié dans chaque formulaire, comparé à chaque POST. Il ne vaut rien
// pour un attaquant qui ne lit pas le cookie, et SameSite=Strict s'y ajoute.
// Secure toujours : openCloud se sert en HTTPS derrière Traefik, et le
// navigateur accepte un cookie Secure sur localhost pour le développement.
func (s *Server) csrfCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMachineRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie(csrfCookieName); err != nil {
			token, err := newCSRFToken()
			if err != nil {
				s.internalError(w, r, err)
				return
			}
			cookie := &http.Cookie{
				Name:     csrfCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				Secure:   true,
				SameSite: http.SameSiteStrictMode,
			}
			http.SetCookie(w, cookie)
			// Le même passage rend déjà un formulaire : il doit voir le jeton.
			r.AddCookie(cookie)
		}
		next.ServeHTTP(w, r)
	})
}

func newCSRFToken() (string, error) {
	raw := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func csrfToken(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// checkCSRF refuse le POST et répond 403 quand le champ ne vaut pas le cookie.
func (s *Server) checkCSRF(w http.ResponseWriter, r *http.Request) bool {
	expected := csrfToken(r)
	given := r.FormValue(csrfFieldName)
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(given)) != 1 {
		s.refuse(w, r, http.StatusForbidden, s.catalog().Get("error.forbidden"))
		return false
	}
	return true
}

// Un agent ou un cron n'a que faire d'un cookie : aucun n'est posé sous
// /agent ni /ping, et leurs POST ne portent pas de jeton anti-CSRF.
func isMachineRoute(path string) bool {
	return strings.HasPrefix(path, "/agent/") || strings.HasPrefix(path, "/ping/")
}
