package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

const backupUsage = `Usage : {command} backup restore -key <clé.age> -state <dossier> <sauvegarde.tar.age>

  restore   restaure une sauvegarde chiffrée de la base, hors ligne : arrêtez
            le serveur avant. Le dossier d'état doit être vide ou absent (il
            est créé en 0700). La phrase de passe se lit sur l'entrée
            standard, une ligne, jamais en argument.
`

// maxPassphraseLine borne la ligne lue : la phrase fait 1024 caractères au
// plus, à 4 octets chacun, plus la fin de ligne.
const maxPassphraseLine = 4*backup.MaxPassphrase + 2

// maxSealedKeyFile borne la clé scellée lue : quelques centaines d'octets.
const maxSealedKeyFile = 64 << 10

func runBackup(args []string) error {
	return backupCommand(args, os.Stdin, os.Stdout, os.Stderr, stdinIsTerminal())
}

// backupCommand aiguille vers la sous-commande. Elle n'ouvre pas la base :
// la restauration ne touche qu'au dossier d'état qu'on lui donne.
func backupCommand(args []string, stdin io.Reader, stdout, stderr io.Writer, isTerminal bool) error {
	if len(args) == 0 || args[0] != "restore" {
		return &refusal{strings.ReplaceAll(backupUsage, "{command}", brand.Command)}
	}
	return restoreCommand(args[1:], stdin, stdout, stderr, isTerminal)
}

func restoreCommand(args []string, stdin io.Reader, stdout, stderr io.Writer, isTerminal bool) error {
	flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	keyPath := flags.String("key", "", "")
	stateDir := flags.String("state", "", "")
	if err := flags.Parse(args); err != nil {
		return &refusal{err.Error()}
	}
	if *keyPath == "" || *stateDir == "" || flags.NArg() != 1 {
		return &refusal{strings.ReplaceAll(backupUsage, "{command}", brand.Command)}
	}
	archivePath := flags.Arg(0)

	// Rien ne s'écrit avant que la phrase n'ait ouvert la clé : un dossier
	// plein ou une phrase fausse ne laissent rien derrière eux.
	if err := requireEmptyOrAbsent(*stateDir); err != nil {
		return err
	}
	sealed, err := readBounded(*keyPath, maxSealedKeyFile)
	if err != nil {
		return &refusal{fmt.Sprintf("clé scellée illisible : %v", err)}
	}
	passphrase, err := readPassphrase(stdin, stderr, isTerminal)
	if err != nil {
		return err
	}
	identity, err := backup.OpenIdentity(sealed, passphrase)
	if errors.Is(err, backup.ErrWrongPassphrase) {
		return &refusal{"la phrase de passe n'ouvre pas cette clé"}
	}
	if err != nil {
		return &refusal{fmt.Sprintf("clé scellée illisible : %v", err)}
	}

	archive, err := os.Open(archivePath) // #nosec G304 G703 -- le fichier que l'opérateur nomme en argument, lu seulement
	if err != nil {
		return &refusal{fmt.Sprintf("sauvegarde illisible : %v", err)}
	}
	defer archive.Close()
	if err := os.MkdirAll(*stateDir, stateDirMode); err != nil { // #nosec G703 -- le dossier -state que l'opérateur nomme, vide ou absent (vérifié plus haut)
		return fmt.Errorf("create state dir: %w", err)
	}
	target, err := os.OpenRoot(*stateDir)
	if err != nil {
		return fmt.Errorf("open state dir: %w", err)
	}
	defer target.Close()
	if err := backup.ExtractArchive(archive, identity, target); err != nil {
		return &refusal{fmt.Sprintf("restauration refusée, rien n'a été gardé : %v", err)}
	}
	fmt.Fprintf(stdout, "Restauré dans %s : %s, %s, %s (0600).\n", *stateDir, brand.DatabaseFile, brand.SecretKeyFile, brand.ServerKeyFile)
	fmt.Fprintf(stdout, "Reste à faire :\n  chown -R %s:%s %s\n  systemctl start %s\nLes agents se reconnectent seuls : la clé qui signe les ordres est celle de %s.\n",
		brand.ID, brand.ID, *stateDir, brand.ServerUnit, brand.ServerKeyFile)
	return nil
}

// requireEmptyOrAbsent refuse un dossier d'état qui porte déjà quelque chose :
// restaurer par-dessus une base vivante la détruirait.
func requireEmptyOrAbsent(path string) error {
	entries, err := os.ReadDir(path) // #nosec G304 G703 -- le dossier que l'opérateur nomme en argument
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &refusal{fmt.Sprintf("dossier d'état illisible : %v", err)}
	}
	if len(entries) > 0 {
		return &refusal{fmt.Sprintf("%s n'est pas vide : restaurez dans un dossier vide ou absent", path)}
	}
	return nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 G703 -- le fichier que l'opérateur nomme en argument, lu et borné
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("plus de %d octets", limit)
	}
	return content, nil
}

// readPassphrase lit une ligne : l'invite va sur la sortie d'erreur quand
// l'entrée est un terminal, et seulement alors.
func readPassphrase(stdin io.Reader, stderr io.Writer, isTerminal bool) (string, error) {
	if isTerminal {
		fmt.Fprint(stderr, "Phrase de passe : ")
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, maxPassphraseLine)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	passphrase := strings.TrimRight(line, "\r\n")
	if passphrase == "" {
		return "", &refusal{"aucune phrase de passe sur l'entrée standard"}
	}
	return passphrase, nil
}
