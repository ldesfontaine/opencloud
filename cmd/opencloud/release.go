package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// #nosec G101 -- le texte d'aide de la commande, pas un secret.
const releaseUsage = `Usage : {command} release <sous-commande>

  verify [-sums F] [-sig F] <fichier>
                      vérifie qu'un fichier appartient à une version signée :
                      la signature de SHA256SUMS (cherché à côté du fichier,
                      avec SHA256SUMS.minisig), puis la somme du fichier.
                      Affiche « ok <version> <nom> sha256:<somme> »
  import [-config F] <dossier>
                      pour mettre à jour les agents : vérifie une version
                      téléchargée (SHA256SUMS, sa signature et les binaires
                      dans <dossier>) puis la copie dans state_dir/releases.
                      Se lance au compte du serveur ; importer deux fois la
                      même version ne change rien
  sign -key-env NOM -version vX.Y.Z <SHA256SUMS>
                      pour publier une version : signe le fichier de sommes
                      avec la clé privée lue dans la variable NOM et écrit
                      SHA256SUMS.minisig à côté
  keygen -o <fichier> pour publier une version : tire une paire de clés,
                      écrit la clé privée dans le fichier (refuse s'il
                      existe) et affiche la clé publique
`

// #nosec G101 -- le nom d'un secret du dépôt, pas sa valeur.
const keygenAdvice = "Posez la clé privée dans le secret RELEASE_SIGNING_KEY du dépôt, gardez-en une copie dans votre coffre, puis collez la clé publique dans internal/release/key.go."

// opencloud release : vérifier une version téléchargée, ou en signer une.
// La clé de confiance est celle que le binaire embarque ; les tests en
// injectent une.
func runRelease(args []string) error {
	return releaseCommand(args, os.Stdout, os.Getenv, release.TrustedKey)
}

func releaseCommand(args []string, out io.Writer, getenv func(string) string, trusted func() (release.PublicKey, error)) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(releaseUsage, "{command}", brand.Command))
		return &refusal{message: "sous-commande manquante"}
	}
	command := args[0]
	if !slices.Contains([]string{"verify", "import", "sign", "keygen"}, command) {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(releaseUsage, "{command}", brand.Command))
		return &refusal{message: "sous-commande inconnue : " + command}
	}
	flags := flag.NewFlagSet("release "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sumsPath := flags.String("sums", "", "verify : le fichier de sommes (par défaut SHA256SUMS à côté du fichier)")
	signaturePath := flags.String("sig", "", "verify : la signature des sommes (par défaut SHA256SUMS.minisig à côté du fichier)")
	keyEnv := flags.String("key-env", "", "sign : la variable d'environnement qui porte la clé privée")
	version := flags.String("version", "", "sign : la version, vX.Y.Z")
	keyFile := flags.String("o", "", "keygen : le fichier où écrire la clé privée")
	configPath := flags.String("config", config.DefaultPath, "import : le fichier de configuration, qui dit où est state_dir")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	switch command {
	case "verify":
		return verifyRelease(flags.Args(), *sumsPath, *signaturePath, out, trusted)
	case "import":
		return importRelease(flags.Args(), *configPath, out, trusted)
	case "sign":
		return signRelease(flags.Args(), *keyEnv, *version, getenv)
	}
	return generateReleaseKey(flags.Args(), *keyFile, out)
}

func verifyRelease(args []string, sumsPath, signaturePath string, out io.Writer, trusted func() (release.PublicKey, error)) error {
	if len(args) != 1 {
		return &refusal{message: "verify attend un fichier"}
	}
	key, err := embeddedKey(trusted)
	if err != nil {
		return err
	}
	file := args[0]
	if sumsPath == "" {
		sumsPath = filepath.Join(filepath.Dir(file), brand.ReleaseSumsFile)
	}
	if signaturePath == "" {
		signaturePath = filepath.Join(filepath.Dir(file), brand.ReleaseSignatureFile)
	}
	signed, err := release.VerifyFile(file, sumsPath, signaturePath, key)
	if err != nil {
		return refuseVerification(err, file)
	}
	name := filepath.Base(file)
	_, err = fmt.Fprintf(out, "ok %s %s sha256:%s\n", signed.Version, name, signed.Sums[name])
	return err
}

// embeddedKey rend la clé de version du binaire, ou le refus qui dit qu'il
// n'en a pas.
func embeddedKey(trusted func() (release.PublicKey, error)) (release.PublicKey, error) {
	key, err := trusted()
	if errors.Is(err, release.ErrNoTrustedKey) {
		return release.PublicKey{}, &refusal{message: "ce binaire n'embarque pas de clé de version : il ne peut rien vérifier"}
	}
	if err != nil {
		return release.PublicKey{}, fmt.Errorf("embedded release key: %w", err)
	}
	return key, nil
}

// importRelease copie une version vérifiée dans la bibliothèque du serveur :
// c'est de là que les agents la téléchargent, sans accès à Internet. Elle
// n'ouvre pas la base, donc n'écrit pas au journal d'audit.
func importRelease(args []string, configPath string, out io.Writer, trusted func() (release.PublicKey, error)) error {
	if len(args) != 1 {
		return &refusal{message: "import attend le dossier de la version"}
	}
	key, err := embeddedKey(trusted)
	if err != nil {
		return err
	}
	cfg, _, err := config.Load(configPath)
	if err != nil {
		return err
	}
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()
	signed, err := release.NewLibrary(stateDir, key).Import(args[0])
	if err != nil {
		return refuseImport(err, args[0])
	}
	_, err = fmt.Fprintf(out, "importé %s : %s\n", signed.Version, strings.Join(signed.BinaryNames(), ", "))
	return err
}

// refuseImport dit à l'opérateur pourquoi la version n'est pas importée ; une
// erreur qui n'est pas un refus de la version reste une erreur.
func refuseImport(err error, dir string) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return &refusal{message: fmt.Sprintf("droits insuffisants (%v) : lancez la commande avec le compte qui lance le serveur, qui possède state_dir", err)}
	case errors.Is(err, fs.ErrNotExist):
		return &refusal{message: fmt.Sprintf("fichier introuvable (%v) : %s doit contenir %s, %s et chaque binaire que les sommes listent",
			err, dir, brand.ReleaseSumsFile, brand.ReleaseSignatureFile)}
	case errors.Is(err, release.ErrWrongKey):
		return &refusal{message: "la signature n'a pas été faite par la clé de version de ce binaire : n'importez pas cette version"}
	case errors.Is(err, release.ErrBadSignature):
		return &refusal{message: fmt.Sprintf("la signature de %s est fausse : le fichier a été modifié ou ne vient pas du projet, n'importez pas cette version", brand.ReleaseSumsFile)}
	case errors.Is(err, release.ErrDigestMismatch):
		return &refusal{message: fmt.Sprintf("un binaire n'a pas la somme de la version signée (%v) : il est abîmé ou modifié, n'importez pas cette version", err)}
	case errors.Is(err, release.ErrVersionConflict):
		return &refusal{message: fmt.Sprintf("%v : une version ne se réécrit jamais, vérifiez que ce dossier est bien celui de la version", err)}
	case errors.Is(err, release.ErrNothingToImport):
		return &refusal{message: fmt.Sprintf("%s ne liste aucun binaire (%s…)", brand.ReleaseSumsFile, brand.ReleaseBinaryPrefix)}
	case errors.Is(err, release.ErrMalformed), errors.Is(err, release.ErrLegacyAlgorithm):
		return &refusal{message: fmt.Sprintf("%s ou %s n'est pas une signature de version lisible (%v)", brand.ReleaseSumsFile, brand.ReleaseSignatureFile, err)}
	}
	return err
}

// refuseVerification dit à l'opérateur ce qui ne va pas ; une erreur qui n'est
// pas une vérification manquée (disque, droits) reste une erreur.
func refuseVerification(err error, file string) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &refusal{message: fmt.Sprintf("fichier introuvable (%v) : %s, %s et %s doivent être côte à côte, ou donnez -sums et -sig",
			err, filepath.Base(file), brand.ReleaseSumsFile, brand.ReleaseSignatureFile)}
	case errors.Is(err, release.ErrWrongKey):
		return &refusal{message: "la signature n'a pas été faite par la clé de version de ce binaire : n'utilisez pas ce fichier"}
	case errors.Is(err, release.ErrBadSignature):
		return &refusal{message: fmt.Sprintf("la signature de %s est fausse : %s a été modifié ou ne vient pas du projet, n'utilisez pas ce fichier",
			brand.ReleaseSumsFile, brand.ReleaseSumsFile)}
	case errors.Is(err, release.ErrNotListed):
		return &refusal{message: fmt.Sprintf("%s n'est pas dans %s : gardez le nom que le fichier a sur la page de la version (%s…)",
			filepath.Base(file), brand.ReleaseSumsFile, brand.ReleaseBinaryPrefix)}
	case errors.Is(err, release.ErrDigestMismatch):
		return &refusal{message: fmt.Sprintf("la somme de %s n'est pas celle de la version signée : le fichier est abîmé ou modifié, n'utilisez pas ce fichier", filepath.Base(file))}
	case errors.Is(err, release.ErrMalformed), errors.Is(err, release.ErrLegacyAlgorithm):
		return &refusal{message: fmt.Sprintf("%s ou %s n'est pas une signature de version lisible (%v)", brand.ReleaseSumsFile, brand.ReleaseSignatureFile, err)}
	}
	return err
}

func signRelease(args []string, keyEnv, version string, getenv func(string) string) error {
	if len(args) != 1 {
		return &refusal{message: "sign attend le fichier de sommes"}
	}
	if keyEnv == "" || version == "" {
		return &refusal{message: "sign demande -key-env et -version"}
	}
	if _, err := release.ParseVersion(version); err != nil {
		return &refusal{message: "la version doit s'écrire vX.Y.Z (sans zéro de tête ni suffixe)"}
	}
	keyText := strings.TrimSpace(getenv(keyEnv))
	if keyText == "" {
		return &refusal{message: fmt.Sprintf("la variable %s est vide ou absente : elle porte la clé privée de la version", keyEnv)}
	}
	// L'erreur ne reprend jamais le texte de la clé.
	key, err := release.ParsePrivateKey(keyText)
	if err != nil {
		return &refusal{message: fmt.Sprintf("la variable %s ne contient pas une clé privée de version", keyEnv)}
	}

	sumsPath := args[0]
	sums, err := os.ReadFile(sumsPath) // #nosec G304 G703 -- chemin donné par l'opérateur, qui signe ses propres fichiers
	if err != nil {
		return fmt.Errorf("read sums: %w", err)
	}
	// Signer des sommes que personne ne saurait relire publierait une version
	// inutilisable, sans retour possible.
	if _, err := release.ParseSums(sums); err != nil {
		return &refusal{message: fmt.Sprintf("%s n'est pas un fichier de sommes (%v)", filepath.Base(sumsPath), err)}
	}
	comment := fmt.Sprintf("timestamp:%d\tfile:%s\tversion:%s", time.Now().Unix(), brand.ReleaseSumsFile, version)
	signature, err := release.Sign(sums, key, comment)
	if err != nil {
		return fmt.Errorf("sign sums: %w", err)
	}
	return writeBesides(sumsPath, ".minisig", signature, 0o644, false)
}

func generateReleaseKey(args []string, keyFile string, out io.Writer) error {
	if len(args) != 0 || keyFile == "" {
		return &refusal{message: "keygen demande -o <fichier> et rien d'autre"}
	}
	publicKey, privateKey, err := release.GenerateKey()
	if err != nil {
		return err
	}
	err = writeBesides(keyFile, "", []byte(privateKey.Encode()+"\n"), 0o600, true)
	if errors.Is(err, fs.ErrExist) {
		return &refusal{message: fmt.Sprintf("%s existe déjà : keygen n'écrase jamais une clé", keyFile)}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Clé privée écrite : %s\nClé publique : %s\n%s\n", keyFile, publicKey, keygenAdvice)
	return err
}

// writeBesides écrit path+suffix : sous un os.Root du dossier de path, par le
// seul écrivain atomique du projet. exclusive refuse un fichier déjà là.
func writeBesides(path, suffix string, content []byte, perm os.FileMode, exclusive bool) error {
	dir, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open directory: %w", err)
	}
	defer dir.Close()
	name := filepath.Base(path) + suffix
	if exclusive {
		return fsx.CreateExclusive(dir, name, content, perm)
	}
	return fsx.WriteAtomic(dir, name, content, perm)
}
