package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/manifest"
)

const applyUsage = `Usage : {command} apply [-config chemin] [-prune] [-yes] [-dry-run] <fichier>

  Lit un fichier YAML (celui que « {command} export » écrit), montre ce qu'il
  changerait dans l'annuaire, demande confirmation, puis l'écrit. Le serveur
  doit tourner : la commande lui parle par la socket de state_dir, pour que
  les agents reçoivent leurs sondes.

  -prune     supprime aussi ce que le fichier ne nomme pas (jamais une machine)
  -yes       confirme sans poser la question (obligatoire sans terminal)
  -dry-run   montre le plan et s'arrête
  -config    chemin du fichier de configuration

  Une application arrêtée au milieu se reprend en relançant la même
  commande : ce qui est écrit ressort « inchangé ».
  Codes de sortie : 0 appliqué ou rien à changer ; 1 erreur ou application
  arrêtée ; 2 fichier refusé ou plan non confirmé.
`

// Les textes propres à la commande sont en français, comme ceux des autres
// sous-commandes ; ce que le plan nomme (genres, conséquences, refus) est
// rendu avec le catalogue, dans la langue de l'interface.
const (
	confirmQuestion = "Appliquer ces changements ? [o/N] "
	nothingToChange = "Rien à changer."
)

// planView est la réponse de /api/manifest/plan, et le plan que les refus
// 409 et 422 portent.
type planView struct {
	Language   string `json:"language"`
	FileHash   string `json:"file_hash"`
	Prune      bool   `json:"prune"`
	PlanHash   string `json:"plan_hash"`
	Applicable bool   `json:"applicable"`
	Summary    struct {
		Create    int `json:"create"`
		Update    int `json:"update"`
		Delete    int `json:"delete"`
		Unchanged int `json:"unchanged"`
		Unmanaged int `json:"unmanaged"`
	} `json:"summary"`
	Changes  []changeView `json:"changes"`
	Problems []struct {
		Key    string `json:"key"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Path   string `json:"path"`
		Value  string `json:"value"`
	} `json:"problems"`
}

type changeView struct {
	Action string `json:"action"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Fields []struct {
		Field  string `json:"field"`
		Before string `json:"before"`
		After  string `json:"after"`
	} `json:"fields"`
	Notes   []string `json:"notes"`
	Carries []struct {
		What  string `json:"what"`
		Count int    `json:"count"`
	} `json:"carries"`
}

// applyView est la réponse 201 de /api/manifest/apply.
type applyView struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Done    int    `json:"done"`
	Error   string `json:"error"`
	Object  string `json:"error_object"`
	Summary struct {
		Create int `json:"create"`
		Update int `json:"update"`
		Delete int `json:"delete"`
	} `json:"summary"`
}

// refusalView est un refus du serveur : une clé, et parfois le plan.
type refusalView struct {
	Error string    `json:"error"`
	Plan  *planView `json:"plan"`
}

func runApply(args []string) error {
	return applyCommand(args, os.Stdin, os.Stdout, stdinIsTerminal())
}

// stdinIsTerminal dit si quelqu'un peut répondre à la question : un tube, un
// fichier ou /dev/null (un cron, un service) ne le peut pas.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(info, null)
}

// applyCommand juge un fichier, le montre, et l'applique si on le confirme.
// Elle n'ouvre pas la base : tout passe par le serveur, qui écrit par les
// gestes de l'interface.
func applyCommand(args []string, in io.Reader, out io.Writer, interactive bool) error {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { fmt.Fprint(os.Stderr, strings.ReplaceAll(applyUsage, "{command}", brand.Command)) }
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	prune := flags.Bool("prune", false, "supprime ce que le fichier ne nomme pas")
	yes := flags.Bool("yes", false, "confirme sans poser la question")
	dryRun := flags.Bool("dry-run", false, "montre le plan et s'arrête")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return &refusal{message: err.Error()}
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return &refusal{message: "un seul fichier à appliquer, après les options"}
	}
	file, err := readManifestFile(flags.Arg(0))
	if err != nil {
		return err
	}
	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	catalogs, err := lang.Load()
	if err != nil {
		return err
	}
	run := &applyRun{
		client: newControlClient(cfg.StateDir), catalogs: catalogs, out: out, in: bufio.NewReader(in),
		file: file, name: filepath.Base(flags.Arg(0)), prune: *prune, yes: *yes, dryRun: *dryRun, interactive: interactive,
		actor: currentAccount(),
	}
	return run.execute(context.Background())
}

func readManifestFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if info.Size() > manifest.MaxFileBytes {
		return nil, &refusal{message: fmt.Sprintf("%s dépasse %d Kio, la taille que la lecture accepte", path, manifest.MaxFileBytes>>10)}
	}
	data, err := os.ReadFile(path) // #nosec G304 -- le fichier que l'opérateur nomme sur sa ligne de commande.
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// currentAccount est le compte système qui lance la commande : il est écrit
// au journal, à titre indicatif.
func currentAccount() string {
	if current, err := user.Current(); err == nil && current.Username != "" {
		return current.Username
	}
	return os.Getenv("USER")
}

type applyRun struct {
	client      *controlClient
	catalogs    lang.Catalogs
	out         io.Writer
	in          *bufio.Reader
	file        []byte
	name        string
	prune       bool
	yes         bool
	dryRun      bool
	interactive bool
	actor       string
}

func (r *applyRun) request(planHash string) map[string]any {
	return map[string]any{
		"file": string(r.file), "file_name": r.name, "prune": r.prune, "plan_hash": planHash, "actor_name": r.actor,
	}
}

// execute enchaîne le plan, la confirmation, l'application. Quand le plan a
// changé entre la confirmation et l'écriture, il montre le nouveau et
// redemande, une seule fois, mais seulement en interactif : quelqu'un lit le
// nouveau plan et le confirme. Sous -yes, personne ne l'a lu : il est montré,
// rien n'est écrit, la commande sort en erreur et se relance. Un annuaire qui
// bouge sans cesse n'est pas appliqué sur la foi d'un plan périmé.
func (r *applyRun) execute(ctx context.Context) error {
	plan, err := r.plan(ctx)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if len(plan.Problems) > 0 {
			return r.refuseFile(plan)
		}
		r.show(plan)
		if len(plan.Changes) == 0 {
			fmt.Fprintln(r.out, nothingToChange)
			return nil
		}
		if r.dryRun {
			fmt.Fprintln(r.out, "Simulation : rien n'est écrit.")
			return nil
		}
		if err := r.confirm(plan); err != nil {
			return err
		}
		status, body, err := r.client.post(ctx, "/api/manifest/apply", r.request(plan.PlanHash), applyTimeout)
		if err != nil {
			return err
		}
		if status == 201 {
			return r.report(plan, body)
		}
		var refused refusalView
		if json.Unmarshal(body, &refused) != nil || refused.Error == "" {
			return serverRefusal(status, body)
		}
		switch refused.Error {
		case manifest.KeyPlanChanged:
			if refused.Plan == nil {
				return fmt.Errorf("%s : l'annuaire a changé pendant la commande, relancez-la", r.text(plan.Language, refused.Error))
			}
			if r.yes || attempt > 0 {
				// -yes confirme le plan qu'on a lu au lancement, pas un autre :
				// le nouveau plan se montre, rien n'est écrit, et on relance.
				fmt.Fprintf(r.out, "%s\n\n", r.text(refused.Plan.Language, refused.Error))
				r.show(*refused.Plan)
				return fmt.Errorf("l'annuaire a changé pendant la commande : relancez-la")
			}
			fmt.Fprintf(r.out, "%s\n\n", r.text(refused.Plan.Language, refused.Error))
			plan = *refused.Plan
		case manifest.KeyNotApplicable:
			if refused.Plan == nil {
				return serverRefusal(status, body)
			}
			return r.refuseFile(*refused.Plan)
		case manifest.KeyNothingToApply:
			fmt.Fprintln(r.out, nothingToChange)
			return nil
		default:
			return fmt.Errorf("%s", r.text(plan.Language, refused.Error))
		}
	}
}

// plan demande au serveur ce que le fichier changerait.
func (r *applyRun) plan(ctx context.Context) (planView, error) {
	status, body, err := r.client.post(ctx, "/api/manifest/plan", r.request(""), controlTimeout)
	if err != nil {
		return planView{}, err
	}
	var plan planView
	if status != 200 || json.Unmarshal(body, &plan) != nil {
		var refused refusalView
		if json.Unmarshal(body, &refused) == nil && refused.Error != "" {
			return planView{}, fmt.Errorf("%s", r.text("", refused.Error))
		}
		return planView{}, serverRefusal(status, body)
	}
	return plan, nil
}

// confirm demande l'accord de l'opérateur. Sans -yes et sans terminal, il n'y
// a personne à qui demander : refus, rien ne s'applique jamais sans
// confirmation.
func (r *applyRun) confirm(plan planView) error {
	if r.yes {
		return nil
	}
	if !r.interactive {
		return &refusal{message: "rien ne s'applique sans confirmation : lancez la commande dans un terminal, ou ajoutez -yes"}
	}
	fmt.Fprint(r.out, confirmQuestion)
	answer, _ := r.in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "o", "oui", "y", "yes":
		return nil
	}
	return &refusal{message: "plan non confirmé : rien n'a été écrit"}
}

// report dit comment l'application s'est finie. Un arrêt au milieu nomme le
// changement refusé, ce qui est écrit, et comment reprendre.
func (r *applyRun) report(plan planView, body []byte) error {
	var applied applyView
	if err := json.Unmarshal(body, &applied); err != nil {
		return fmt.Errorf("read the server's answer: %w", err)
	}
	total := applied.Summary.Create + applied.Summary.Update + applied.Summary.Delete
	if applied.State == string(manifest.ApplyApplied) {
		fmt.Fprintf(r.out, "Appliqué : %s (%s).\n", countText(applied.Done, "changement"), applied.ID)
		return nil
	}
	reason := r.text(plan.Language, applied.Error)
	message := fmt.Sprintf("Arrêté au changement %d/%d (%s) : %s", applied.Done+1, total, r.objectText(plan.Language, applied.Object), reason)
	switch {
	case applied.Done > 1:
		message += fmt.Sprintf("\nLes %d premiers sont écrits ; ", applied.Done)
	case applied.Done == 1:
		message += "\nLe premier est écrit ; "
	default:
		message += "\nRien n'est écrit ; "
	}
	message += fmt.Sprintf("corrigez puis relancez : %s reprend où il s'est arrêté (%s).", brand.Command+" apply", applied.ID)
	return fmt.Errorf("%s", message)
}

func countText(count int, noun string) string {
	if count > 1 {
		return fmt.Sprintf("%d %ss écrits", count, noun)
	}
	return fmt.Sprintf("%d %s écrit", count, noun)
}

// refuseFile montre chaque refus du fichier, à sa ligne, puis sort en refus :
// rien n'a été écrit.
func (r *applyRun) refuseFile(plan planView) error {
	catalog := r.catalogs.For(planLanguage(plan.Language))
	for _, item := range plan.Problems {
		fmt.Fprintf(r.out, "  %s\n", problemText(catalog, manifest.Problem{
			Key: item.Key, Line: item.Line, Column: item.Column, Path: item.Path, Value: item.Value,
		}))
	}
	return &refusal{message: fmt.Sprintf("%d refus dans %s : rien n'a été écrit", len(plan.Problems), r.name)}
}

// problemText écrit un refus comme l'opérateur le lit. Le texte d'une clé du
// fichier prend ses arguments (ligne, valeur) ; celui d'un domaine n'en prend
// aucun, la ligne et l'entrée le précèdent.
func problemText(catalog lang.Catalog, problem manifest.Problem) string {
	if !problem.IsDomainRefusal() {
		return catalog.Format(problem.Key, problem.Arguments()...)
	}
	text := catalog.Get(problem.Key)
	switch {
	case problem.Line > 0 && problem.Path != "":
		return fmt.Sprintf("Ligne %d (%s) : %s", problem.Line, problem.Path, text)
	case problem.Line > 0:
		return fmt.Sprintf("Ligne %d : %s", problem.Line, text)
	}
	return text
}

// countedWord écrit « 1 inchangé », « 3 inchangés » : le nombre avec son
// séparateur de milliers, puis le mot accordé.
func countedWord(code lang.Code, count int, singular, plural string) string {
	word := plural
	if count == 1 || count == 0 {
		word = singular
	}
	return lang.FormatCount(code, count) + " " + word
}

func planLanguage(code string) lang.Code {
	if parsed, ok := lang.Parse(code); ok {
		return parsed
	}
	return lang.Default
}

// text rend une clé du catalogue dans la langue de l'interface.
func (r *applyRun) text(code, key string) string {
	if key == "" {
		key = manifest.KeyApplyFailed
	}
	return r.catalogs.For(planLanguage(code)).Get(key)
}

// objectText écrit « genre/nom » comme « sonde wiki-http ».
func (r *applyRun) objectText(code, object string) string {
	kind, name, found := strings.Cut(object, "/")
	if !found {
		return object
	}
	return r.kindText(code, kind) + " " + name
}

func (r *applyRun) kindText(code, kind string) string {
	text := r.catalogs.For(planLanguage(code)).Get("manifest.kind_" + kind)
	runes := []rune(text)
	if len(runes) > 0 {
		runes[0] = unicode.ToLower(runes[0])
	}
	return string(runes)
}

// show écrit le plan : chaque changement dans l'ordre où il s'écrira, puis le
// compte de ce qui reste.
func (r *applyRun) show(plan planView) {
	if len(plan.Changes) > 0 {
		fmt.Fprintf(r.out, "Plan pour %s (%s)\n", r.name, shortHash(plan.FileHash))
	}
	catalog := r.catalogs.For(planLanguage(plan.Language))
	for _, change := range plan.Changes {
		r.showChange(catalog, plan.Language, change)
	}
	code := planLanguage(plan.Language)
	footer := "  = " + countedWord(code, plan.Summary.Unchanged, "inchangé", "inchangés")
	if plan.Summary.Unmanaged > 0 {
		footer += " · " + countedWord(code, plan.Summary.Unmanaged, "hors fichier, gardé", "hors fichier, gardés") + " (-prune pour les supprimer)"
	}
	fmt.Fprintln(r.out, footer)
}

func (r *applyRun) showChange(catalog lang.Catalog, code string, change changeView) {
	label := r.kindText(code, change.Kind) + " " + change.Name
	switch change.Action {
	case string(manifest.ActionCreate):
		fmt.Fprintf(r.out, "  + %s\n", label)
	case string(manifest.ActionDelete):
		line := fmt.Sprintf("  - %s", label)
		if change.ID != "" {
			line += fmt.Sprintf(" (%s)", shortID(change.ID))
		}
		var carries []string
		for _, carry := range change.Carries {
			carries = append(carries, catalog.Counted("manifest.carries_"+carry.What, carry.Count))
		}
		if len(carries) > 0 {
			line += " — emporte " + strings.Join(carries, ", ")
		}
		fmt.Fprintln(r.out, line)
	default:
		if len(change.Fields) == 1 && len(change.Notes) == 0 {
			field := change.Fields[0]
			fmt.Fprintf(r.out, "  ~ %s : %s %s → %s\n", label, field.Field, valueText(field.Before), valueText(field.After))
			return
		}
		line := "  ~ " + label
		if change.ID != "" {
			line += fmt.Sprintf(" (%s)", shortID(change.ID))
		}
		fmt.Fprintln(r.out, line)
		for _, field := range change.Fields {
			fmt.Fprintf(r.out, "      %s\n", fieldText(field.Field, field.Before, field.After))
		}
	}
	for _, note := range change.Notes {
		fmt.Fprintf(r.out, "      ! %s\n", catalog.Get("manifest.note_"+note))
	}
}

// fieldText écrit un champ qui change : « interval : 1m → 30s ». Une valeur
// vide s'écrit « ∅ ».
func fieldText(field, before, after string) string {
	return fmt.Sprintf("%s : %s → %s", field, valueText(before), valueText(after))
}

func valueText(value string) string {
	if value == "" {
		return "∅"
	}
	return value
}

// shortHash coupe « sha256:<64 hex> » à huit caractères utiles.
func shortHash(hash string) string {
	algorithm, digest, found := strings.Cut(hash, ":")
	if !found || len(digest) <= 8 {
		return hash
	}
	return algorithm + ":" + digest[:8] + "…"
}

// shortID garde le préfixe d'un identifiant et le début de son hasard.
func shortID(id string) string {
	prefix, rest, found := strings.Cut(id, "_")
	if found && len(rest) > 4 {
		return prefix + "_" + rest[:4] + "…"
	}
	if len(id) > 4 {
		return id[:4] + "…"
	}
	return id
}
