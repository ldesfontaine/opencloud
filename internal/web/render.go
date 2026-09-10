package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"unicode"

	"github.com/ldesfontaine/opencloud/internal/store"
)

// Un gabarit complet par page : la mise en page plus le contenu de la page.
type pageTemplates map[string]*template.Template

// parsePageTemplates lit chaque page servie, un gabarit sous templates/<nom>.html.
func parsePageTemplates(files fs.FS) (pageTemplates, error) {
	templates := pageTemplates{}
	for _, name := range []string{"login", "password", "infrastructure", "machine", "machine-new",
		"domains", "domains-new", "action-form", "action"} {
		parsed, err := template.ParseFS(files, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		templates[name] = parsed
	}
	return templates, nil
}

// page est ce que voit tout gabarit. Account est nil avant connexion.
type page struct {
	Version   string
	Account   *store.Account
	CSRFToken string
	// L'onglet ouvert : la barre latérale marque le sien.
	Section string
	// L'initiale du compte, en majuscule, pour la pastille du menu.
	AccountInitial string
	// Ce qui a été refusé : la cause, puis le geste qui la lève.
	Error *refusalView
	// Longueur minimale d'un nouveau mot de passe ; 0 quand la règle est levée.
	MinPasswordLength int
	// Ce que la page rend en propre : une vue par gabarit.
	Data any
}

func (s *Server) newPage(r *http.Request, account *store.Account, csrfToken string) page {
	return page{
		Version:           s.version,
		Account:           account,
		CSRFToken:         csrfToken,
		Section:           sectionForPath(r.URL.Path),
		AccountInitial:    accountInitial(account),
		MinPasswordLength: s.auth.PasswordPolicy().MinLength,
	}
}

// Les onglets de la barre latérale ; un onglet, une section.
const (
	sectionInfrastructure = "infrastructure"
	sectionDomains        = "domains"
)

// sectionForPath dit quel onglet est ouvert. Une fiche de machine et une
// action sont sous Infrastructure : l'opérateur y est arrivé par là. Une page
// hors des onglets — le mot de passe — n'en marque aucun.
func sectionForPath(path string) string {
	switch {
	// Les zones vivent dans la vue Domaines : leurs gestes marquent le même
	// onglet.
	case path == "/domains" || strings.HasPrefix(path, "/domains/") ||
		path == "/zones" || strings.HasPrefix(path, "/zones/"):
		return sectionDomains
	case path == "/" || strings.HasPrefix(path, "/machines/") || strings.HasPrefix(path, "/actions/"):
		return sectionInfrastructure
	}
	return ""
}

// accountInitial : la première lettre du compte, en majuscule. Vide avant
// connexion, et vide aussi pour un identifiant qui ne commence par rien.
func accountInitial(account *store.Account) string {
	if account == nil {
		return ""
	}
	for _, letter := range account.Username {
		return string(unicode.ToUpper(letter))
	}
	return ""
}

func (p page) withData(data any) page {
	p.Data = data
	return p
}

func (p page) withError(refused refusalView) page {
	p.Error = &refused
	return p
}

// render écrit d'abord dans un tampon : une erreur de gabarit donne une vraie
// 500, pas une page à moitié envoyée.
func (s *Server) render(w http.ResponseWriter, status int, name string, data page) {
	var buffer bytes.Buffer
	if err := s.templates[name].ExecuteTemplate(&buffer, "layout", data); err != nil {
		s.logger.Error("render page", "page", name, "error", err)
		http.Error(w, messageServerError, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		// Le navigateur est parti avant la fin : rien à réparer.
		s.logger.Debug("write page", "page", name, "error", err)
	}
}

func (s *Server) serverError(w http.ResponseWriter, what string, err error) {
	s.logger.Error(what, "error", err)
	http.Error(w, messageServerError, http.StatusInternalServerError)
}
