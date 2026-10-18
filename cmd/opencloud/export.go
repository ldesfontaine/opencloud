package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const exportUsage = `Usage : {command} export [-config chemin] [-o fichier] [-propose]

  Écrit l'annuaire (projets, services déclarés, sondes, tâches, clés
  ignorées) dans un fichier YAML, sur la sortie standard par défaut. Les
  identifiants y sont, jamais un secret. Le serveur doit tourner : la
  commande lui parle par la socket de state_dir.

  Avec -propose, le fichier gagne à la fin ce que les conteneurs non
  déclarés laissent deviner (services sans identifiant, unités à ajouter à un
  service déclaré), chacun précédé d'un commentaire. Rien n'est déclaré avant
  que vous ayez relu le fichier et lancé {command} apply.
`

// exportFileMode : le fichier ne porte aucun secret, mais il décrit
// l'infrastructure. Qui veut le partager l'ouvre lui-même.
const exportFileMode = 0o600

// opencloud export : l'annuaire en YAML, tel que le serveur le sert à
// l'interface. Elle n'ouvre pas la base : lancée en root, elle ne crée donc
// pas de fichier -wal au nom de root.
func runExport(args []string) error {
	return exportCommand(args, os.Stdout)
}

func exportCommand(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { fmt.Fprint(os.Stderr, strings.ReplaceAll(exportUsage, "{command}", brand.Command)) }
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	outputPath := flags.String("o", "", "fichier à écrire (défaut : la sortie standard)")
	propose := flags.Bool("propose", false, "ajouter les propositions tirées des conteneurs non déclarés")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return &refusal{message: "argument inattendu : " + flags.Arg(0)}
	}
	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	path := "/api/manifest/export"
	if *propose {
		path += "?propose=1"
	}
	data, err := newControlClient(cfg.StateDir).get(context.Background(), path)
	if err != nil {
		return err
	}
	if *outputPath == "" {
		_, err = out.Write(data)
		return err
	}
	if err := writeExportFile(*outputPath, data); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Annuaire écrit dans %s.\n", *outputPath)
	return err
}

// writeExportFile remplace le fichier d'un seul coup : un export interrompu
// ne laisse pas un fichier tronqué à la place de celui qui était versionné.
func writeExportFile(path string, data []byte) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open export directory: %w", err)
	}
	defer root.Close()
	if err := fsx.WriteAtomic(root, filepath.Base(path), data, exportFileMode); err != nil {
		return fmt.Errorf("write export file: %w", err)
	}
	return nil
}
