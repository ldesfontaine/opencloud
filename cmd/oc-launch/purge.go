package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
)

const (
	// systemd tient ce dossier tant qu'il est en route ; sans lui, aucune
	// unité ne tourne sur cette machine.
	systemdRuntimeDirectory = "/run/systemd/system"

	// La question posée à systemd est brève : au-delà, on ne sait pas, et un
	// dossier dont on ne sait rien est gardé.
	unitQuestionTimeout = 5 * time.Second
)

// purger garde les KeptDirectories derniers dossiers d'action et retire les
// autres. Il vit dans le lanceur parce que c'est le seul code qui tourne en
// root sur la machine, au passage de chaque action, et qu'il peut demander à
// systemd ce qui tourne encore (05-execution.md).
type purger struct {
	actionsRoot string
	// isUnitRunning : posée en champ pour que les tests répondent sans systemd.
	isUnitRunning func(unitName string) bool
	out           io.Writer
	errOut        io.Writer
}

func newPurger(actionsRoot string, out, errOut io.Writer) purger {
	systemd := newUnitQuestion()
	return purger{actionsRoot: actionsRoot, isUnitRunning: systemd.isRunning, out: out, errOut: errOut}
}

// run purge, puis dit sur la sortie combien de dossiers sont partis — rien
// s'il n'y en a eu aucun. Une purge qui échoue ne retient pas l'action : elle
// se dit sur l'erreur standard, et le lancement continue.
func (p purger) run(launchedID string) {
	purged, err := p.removeOldDirectories(launchedID)
	if err != nil {
		fmt.Fprintf(p.errOut, "purge des anciens dossiers d'action : %v\n", err)
	}
	if purged > 0 {
		fmt.Fprint(p.out, actiondir.FormatPurged(purged))
	}
}

// actionDirectory : un dossier d'action et sa date de dépôt. C'est elle qui
// trie, jamais le nom : un identifiant ne dit pas son âge.
type actionDirectory struct {
	id          string
	depositedAt time.Time
}

// removeOldDirectories rend le nombre de dossiers retirés. Le dossier de
// l'action qu'on lance est gardé, et un dossier dont l'unité tourne encore
// aussi, même s'il est vieux.
func (p purger) removeOldDirectories(launchedID string) (int, error) {
	root, err := os.OpenRoot(p.actionsRoot)
	if err != nil {
		return 0, fmt.Errorf("open actions directory: %w", err)
	}
	defer root.Close()

	directories, err := listActionDirectories(root)
	if err != nil {
		return 0, err
	}
	// Les plus récents d'abord : ce sont eux qu'on garde.
	slices.SortFunc(directories, func(a, b actionDirectory) int {
		return b.depositedAt.Compare(a.depositedAt)
	})

	purged := 0
	for index, directory := range directories {
		if index < actiondir.KeptDirectories || directory.id == launchedID {
			continue
		}
		if p.isUnitRunning(actiondir.UnitName(directory.id)) {
			continue
		}
		// root.RemoveAll ne sort jamais de /var/lib/opencloud/actions, et le
		// nom est un sous-dossier direct que ValidID vient d'accepter.
		if err := root.RemoveAll(directory.id); err != nil {
			return purged, fmt.Errorf("remove action directory: %w", err)
		}
		purged++
	}
	return purged, nil
}

// listActionDirectories ne retient que les sous-dossiers directs au nom
// d'action valide. Un lien symbolique, un fichier, un nom inattendu : rien
// n'est retenu, donc rien ne sera retiré.
func listActionDirectories(root *os.Root) ([]actionDirectory, error) {
	opened, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open actions directory: %w", err)
	}
	defer opened.Close()

	entries, err := opened.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read actions directory: %w", err)
	}

	var directories []actionDirectory
	for _, entry := range entries {
		// Type() vient de lstat : un lien symbolique, même vers un dossier,
		// n'est jamais un dossier ici — il n'entre donc pas dans la liste.
		if !entry.Type().IsDir() || !actiondir.ValidID(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue // disparu entre-temps : rien à purger
		}
		directories = append(directories, actionDirectory{id: entry.Name(), depositedAt: info.ModTime()})
	}
	return directories, nil
}

// unitQuestion demande à systemd si l'unité d'une action tourne encore.
// systemctlPath vide : systemd n'est pas en route ici, aucune unité ne tourne,
// et rien ne retient un dossier.
type unitQuestion struct {
	systemctlPath string
}

func newUnitQuestion() unitQuestion {
	if _, err := os.Stat(systemdRuntimeDirectory); err != nil {
		return unitQuestion{}
	}
	// Debian récent pose systemctl dans /usr/bin, un système non fusionné
	// dans /bin.
	for _, candidate := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		if _, err := os.Stat(candidate); err == nil {
			return unitQuestion{systemctlPath: candidate}
		}
	}
	return unitQuestion{}
}

func (q unitQuestion) isRunning(unitName string) bool {
	if q.systemctlPath == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), unitQuestionTimeout)
	defer cancel()
	// #nosec G204 -- bounded: chemin constant, et un nom d'unité bâti par
	// actiondir.UnitName sur un identifiant que ValidID a accepté.
	command := exec.CommandContext(ctx, q.systemctlPath, "is-active", "--", unitName+".service")
	command.Env = []string{fixedPath, fixedLocale}

	// is-active sort non nul dès que l'unité n'est pas active : c'est l'état
	// écrit qui répond, pas le code.
	output, err := command.Output()
	state := strings.TrimSpace(string(output))
	if err != nil && state == "" {
		return true // systemd n'a rien dit : on ne sait pas, on garde
	}
	// « inactive » couvre aussi l'unité qui n'existe plus : --collect l'a
	// ramassée à la fin de l'action.
	return state != "inactive" && state != "failed"
}
