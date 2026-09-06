package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Le préfixe __Host- oblige le navigateur à n'accepter le cookie que sur cet
// hôte, en HTTPS, sans domaine ni chemin restreint : un sous-domaine ne peut
// pas en poser un du même nom.
const (
	sessionCookieName = "__Host-opencloud_session"
	csrfCookieName    = "__Host-opencloud_csrf"
	csrfFieldName     = "_csrf"
	csrfKeyBytes      = 32
)

func newCookie(name, value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, newCookie(sessionCookieName, token, expires))
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, newCookie(sessionCookieName, "", time.Unix(0, 0)))
}

func sessionToken(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

// Anti-CSRF par double soumission signée (OWASP, CSRF Prevention Cheat
// Sheet) : le cookie porte une valeur aléatoire, le formulaire porte son HMAC
// avec une clé que seul le serveur connaît. Un site tiers ne peut ni lire le
// cookie, ni produire la signature d'un cookie qu'il aurait réussi à poser.
// La clé est tirée au démarrage : un redémarrage périme les formulaires
// ouverts, qui le disent.
type csrfSigner struct {
	key []byte
}

func newCSRFSigner() (csrfSigner, error) {
	key := make([]byte, csrfKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return csrfSigner{}, fmt.Errorf("generate csrf key: %w", err)
	}
	return csrfSigner{key: key}, nil
}

func (c csrfSigner) sign(cookieValue string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(cookieValue))
	return hex.EncodeToString(mac.Sum(nil))
}

var errCSRFMismatch = errors.New("csrf token mismatch")

// csrfFormToken pose le cookie s'il manque et rend la valeur signée à mettre
// dans le formulaire.
func (s *Server) csrfFormToken(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err == nil && cookie.Value != "" {
		return s.csrf.sign(cookie.Value)
	}
	return s.rotateCSRF(w)
}

// rotateCSRF pose un cookie neuf : à chaque connexion et déconnexion.
func (s *Server) rotateCSRF(w http.ResponseWriter) string {
	cookieValue := rand.Text()
	http.SetCookie(w, newCookie(csrfCookieName, cookieValue, time.Time{}))
	return s.csrf.sign(cookieValue)
}

func (s *Server) verifyCSRF(r *http.Request) error {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return errCSRFMismatch
	}
	expected := s.csrf.sign(cookie.Value)
	submitted := r.PostFormValue(csrfFieldName)
	if !hmac.Equal([]byte(expected), []byte(submitted)) {
		return errCSRFMismatch
	}
	return nil
}
