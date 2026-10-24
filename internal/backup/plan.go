package backup

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

var (
	subjectPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	selectorPattern = regexp.MustCompile(`^\d{1,9}(,\d{1,9})*$`)
)

// Complete pose le nombre gardé par défaut et retire les espaces de bord du
// libellé.
func (d *PlanDefinition) Complete() {
	if d.Keep == 0 {
		d.Keep = DefaultKeep
	}
	d.Name = strings.TrimSpace(d.Name)
}

// Validate rend le premier refus d'un plan, sans rien lire de la base : la
// machine et la destination qu'il nomme se vérifient par le service.
func (d PlanDefinition) Validate() error {
	if !subjectPattern.MatchString(d.Subject) || !isPlainName(d.Name) {
		return ErrPlanInvalid
	}
	switch d.SourceKind {
	case SourceDatabase:
		// La base est le seul plan qui s'appelle « database », et elle n'a
		// ni machine, ni producteur, ni sélecteur.
		if d.Subject != SubjectDatabase || d.SourceMachineID != "" || d.Producer != "" || d.Selector != "" {
			return ErrPlanInvalid
		}
	case SourceAgent:
		if err := d.validateAgentSource(); err != nil {
			return err
		}
	default:
		return ErrPlanInvalid
	}
	if d.Keep < MinKeep || d.Keep > MaxKeep {
		return ErrKeepInvalid
	}
	return nil
}

func (d PlanDefinition) validateAgentSource() error {
	// « opencloud » est le préfixe des fichiers de la base : un plan qui le
	// prendrait serait élagué avec elle.
	if d.Subject == SubjectDatabase || d.Subject == brand.ID {
		return ErrPlanInvalid
	}
	if d.SourceMachineID == "" {
		return ErrSourceMachineUnknown
	}
	if !slices.Contains(Producers(), d.Producer) {
		return ErrProducerUnknown
	}
	if d.Selector != SelectorAll && (len(d.Selector) > MaxSelector || !selectorPattern.MatchString(d.Selector)) {
		return ErrSelectorInvalid
	}
	return nil
}

// isPlainName : un libellé d'une ligne, sans caractère de contrôle.
func isPlainName(name string) bool {
	if utf8.RuneCountInString(name) > MaxName {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}

// sameSource dit si deux plans prennent la même chose, au même endroit : le
// second ne ferait que répéter le premier.
func sameSource(a, b Plan) bool {
	return a.SourceKind == b.SourceKind && a.SourceMachineID == b.SourceMachineID &&
		a.Producer == b.Producer && a.Selector == b.Selector && a.DestinationID == b.DestinationID
}
