package web

import (
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Un rapport, pas un journal : la sortie d'une action conclue relue en
// sections. Les avertissements passent en tête, le reste se range sous
// l'étape qui l'a écrit.
type report struct {
	Warnings []string
	Sections []reportSection
}

type reportSection struct {
	Title string
	Icon  string
	Facts []reportFact
}

type reportFact struct {
	Label string
	Value reportValue
}

// reportValue : la valeur telle qu'elle s'affiche — le texte, sa couleur, la
// marque posée devant, et la valeur entière quand le texte est tronqué.
type reportValue struct {
	Text  string
	Tone  string
	Mark  string
	Title string
	Mono  bool
}

// Les couleurs d'une valeur ; la valeur est aussi la classe CSS.
const (
	toneOK     = "ok"
	toneWarn   = "warn"
	toneDanger = "danger"
	toneAccent = "accent"
)

// Le vocabulaire d'une action : comment se lisent ses étapes et ses clés.
// Une action nouvelle s'ajoute par une entrée dans reportVocabularies, jamais
// par un « if » de plus.
type reportVocabulary struct {
	steps map[string]reportStep
	facts map[string]factReading
}

type reportStep struct {
	Title string
	Icon  string
}

// factReading : le libellé d'une clé, et comment sa valeur se lit. read vaut
// nil quand la valeur s'affiche telle quelle.
type factReading struct {
	Label string
	Read  func(raw string) reportValue
}

var reportVocabularies = map[catalog.Kind]reportVocabulary{
	catalog.KindDiagnostiquer: diagnosticVocabulary(),
}

// newReport interprète la sortie d'une action conclue quand son vocabulaire
// existe, et rend nil sinon : les autres actions gardent leur journal. Une
// sortie sans aucun constat — un refus au préflight — n'est pas un rapport
// non plus.
func newReport(kind catalog.Kind, lines []store.ActionLine) *report {
	vocabulary, found := reportVocabularies[kind]
	if !found {
		return nil
	}

	read := readReport(vocabulary, lines)
	if len(read.Sections) == 0 && len(read.Warnings) == 0 {
		return nil
	}
	return &read
}

// readReport suit la sortie ligne à ligne, dans l'ordre où le script l'écrit
// (15-catalogue-actions.md §1). Une étape sans constat ne fait pas de
// section : le bandeau du résultat dit déjà que l'action est allée au bout.
func readReport(vocabulary reportVocabulary, lines []store.ActionLine) report {
	var read report
	var current reportSection

	closeSection := func() {
		if len(current.Facts) > 0 {
			read.Sections = append(read.Sections, current)
		}
		current = reportSection{}
	}

	for _, line := range lines {
		text := line.Text
		switch {
		case strings.HasPrefix(text, prefixStep):
			closeSection()
			current = vocabulary.section(rest(text, prefixStep))
		case strings.HasPrefix(text, prefixInfo):
			key, raw, found := strings.Cut(rest(text, prefixInfo), "=")
			if !found {
				continue
			}
			if current.Title == "" {
				current = reportSection{Title: labelLooseFacts, Icon: iconInfo}
			}
			current.Facts = append(current.Facts, vocabulary.fact(key, raw))
		case strings.HasPrefix(text, prefixWarning):
			read.Warnings = append(read.Warnings, rest(text, prefixWarning))
		}
	}
	closeSection()
	return read
}

func rest(text, prefix string) string {
	return strings.TrimSpace(strings.TrimPrefix(text, prefix))
}

// Une étape que le vocabulaire ne connaît pas garde son texte : le rapport
// montre ce que le script a écrit, il ne le tait pas.
func (v reportVocabulary) section(step string) reportSection {
	if known, found := v.steps[step]; found {
		return reportSection{Title: known.Title, Icon: known.Icon}
	}
	return reportSection{Title: step, Icon: iconInfo}
}

// Une clé inconnue garde son nom, soulignés remplacés par des espaces.
func (v reportVocabulary) fact(key, raw string) reportFact {
	reading, found := v.facts[key]
	if !found {
		return reportFact{Label: strings.ReplaceAll(key, "_", " "), Value: plainValue(raw)}
	}
	if reading.Read == nil {
		return reportFact{Label: reading.Label, Value: plainValue(raw)}
	}
	return reportFact{Label: reading.Label, Value: reading.Read(raw)}
}

func plainValue(raw string) reportValue {
	return reportValue{Text: raw}
}

// ------------------------------------------------------- Diagnostiquer

// Ce que Diagnostiquer écrit, et comment l'opérateur le lit
// (internal/scripts/diagnostiquer/run.sh).
func diagnosticVocabulary() reportVocabulary {
	return reportVocabulary{
		steps: map[string]reportStep{
			"préflight — de quoi lire la machine": {Title: "Préflight", Icon: iconShield},
			"identité":                            {Title: "Identité", Icon: iconServer},
			"horloge":                             {Title: "Horloge", Icon: iconClock},
			"espace et mémoire":                   {Title: "Espace et mémoire", Icon: iconActivity},
			"unités systemd":                      {Title: "Unités systemd", Icon: iconRefresh},
			"docker":                              {Title: "Docker", Icon: iconTerminal},
			"ports":                               {Title: "Ports", Icon: iconShield},
			"la norme d'implantation":             {Title: "Norme d'implantation", Icon: iconCheck},
			"le lanceur":                          {Title: "Lanceur", Icon: iconKey},
			"vérifier — la lecture n'a rien écrit": {Title: "Vérification", Icon: iconCheck},
		},
		facts: map[string]factReading{
			"hote":                    {Label: "Hôte"},
			"noyau":                   {Label: "Noyau"},
			"systeme":                 {Label: "Système"},
			"duree_de_fonctionnement": {Label: "Allumée depuis", Read: uptimeValue},
			"horloge_synchronisee":    {Label: "Horloge synchronisée", Read: synchronizedValue},
			"espace_libre_racine":     {Label: "Espace libre sur /"},
			"espace_libre_srv":        {Label: "Espace libre sur /srv", Read: srvSpaceValue},
			"memoire_libre":           {Label: "Mémoire libre"},
			"unites_en_echec":         {Label: "Unités en échec", Read: failedUnitsValue},
			"docker":                  {Label: "Docker", Read: dockerValue},
			"plugin_compose":          {Label: "Plugin compose", Read: presenceValue},
			"port_80":                 {Label: "Port 80", Read: portValue},
			"port_443":                {Label: "Port 443", Read: portValue},
			"norme_workspace":         {Label: "/srv/workspace", Read: presenceValue},
			"norme_data":              {Label: "/srv/data", Read: presenceValue},
			"lanceur":                 {Label: "Lanceur", Read: launcherValue},
			"lanceur_proprietaire":    {Label: "Propriétaire du lanceur", Read: monoValue},
			"lanceur_mode":            {Label: "Mode du lanceur", Read: monoValue},
			"lanceur_bits_speciaux":   {Label: "Bits spéciaux"},
			"lanceur_empreinte":       {Label: "Empreinte", Read: digestValue},
		},
	}
}

// Le script écrit « 127289 s » : l'opérateur lit des jours et des heures.
func uptimeValue(raw string) reportValue {
	seconds, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(raw), " s"))
	if err != nil || seconds < 0 {
		return plainValue(raw)
	}

	const hour = 3600
	const day = 24 * hour
	switch {
	case seconds >= day:
		return plainValue(strconv.Itoa(seconds/day) + " j " + strconv.Itoa(seconds%day/hour) + " h")
	case seconds >= hour:
		return plainValue(strconv.Itoa(seconds/hour) + " h " + strconv.Itoa(seconds%hour/60) + " min")
	}
	return plainValue(strconv.Itoa(seconds/60) + " min")
}

// timedatectl répond yes ou no ; sans lui, le script dit « absent ».
func synchronizedValue(raw string) reportValue {
	switch raw {
	case "yes":
		return reportValue{Text: "oui", Tone: toneOK, Mark: iconCheck}
	case "no":
		return reportValue{Text: "non", Tone: toneWarn}
	}
	return reportValue{Text: raw, Tone: toneWarn}
}

// Pas de /srv : la norme d'implantation n'est pas posée, ce n'est pas un
// échec (15-catalogue-actions.md).
func srvSpaceValue(raw string) reportValue {
	if raw == "absent" {
		return reportValue{Text: "/srv absent", Tone: toneWarn}
	}
	return plainValue(raw)
}

func failedUnitsValue(raw string) reportValue {
	switch raw {
	case "aucune":
		return reportValue{Text: "aucune", Tone: toneOK, Mark: iconCheck}
	case "absent":
		return reportValue{Text: "absent", Tone: toneWarn}
	}
	return reportValue{Text: raw, Tone: toneDanger, Mark: iconX}
}

// Un port tenu n'est pas une erreur : c'est un fait à voir.
func portValue(raw string) reportValue {
	switch raw {
	case "tenu":
		return reportValue{Text: "tenu", Tone: toneAccent}
	case "libre":
		return plainValue("libre")
	}
	return reportValue{Text: raw, Tone: toneWarn}
}

// Le script écrit la version de Docker, ou « présent » s'il ne la lit pas, ou
// « absent ». Sans Docker, rien ne se déploie : c'est un avertissement, pas
// un échec — la machine peut n'avoir rien à héberger encore.
func dockerValue(raw string) reportValue {
	if raw == "absent" {
		return reportValue{Text: "absent", Tone: toneWarn}
	}
	return reportValue{Text: raw, Tone: toneOK, Mark: iconCheck}
}

func presenceValue(raw string) reportValue {
	switch raw {
	case "présent":
		return reportValue{Text: "présent", Tone: toneOK, Mark: iconCheck}
	case "absent":
		return reportValue{Text: "absent", Tone: toneWarn}
	}
	return plainValue(raw)
}

// Sans lanceur, aucune action ne part vers la machine : c'est du rouge.
func launcherValue(raw string) reportValue {
	if raw == "absent" {
		return reportValue{Text: "absent", Tone: toneDanger, Mark: iconX}
	}
	return monoValue(raw)
}

func monoValue(raw string) reportValue {
	return reportValue{Text: raw, Mono: true}
}

// L'empreinte tient sur 71 caractères : la grille en montre le début, le
// title porte l'entière.
func digestValue(raw string) reportValue {
	const shown = 20
	if len([]rune(raw)) <= shown {
		return monoValue(raw)
	}
	return reportValue{Text: string([]rune(raw)[:shown]) + "…", Title: raw, Mono: true}
}
