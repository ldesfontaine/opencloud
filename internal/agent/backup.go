package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	// L'agent écrit ce qu'il reçoit en 0600, dans un dossier 0700.
	backupFileMode = 0o600
	backupDirMode  = 0o700
	// Toute la réception se fait en moins que MaxRunDuration (une heure,
	// côté serveur) : un téléchargement qui cale ne bloque pas les ordres
	// suivants au-delà.
	receiveTimeout = 50 * time.Minute
	maxParcelFiles = 8
	// L'en-tête d'un fichier age binaire.
	ageHeader = "age-encryption.org/v1\n"
	// Pendant le téléchargement, le fichier s'appelle « .<nom>.part ».
	partPrefix = "."
	partSuffix = ".part"
	// Un résultat porte au plus MaxOutputLines lignes ; la dernière dit combien
	// d'effacements n'ont pas été nommés.
	maxPrunedListed = agentwire.MaxOutputLines - 4
)

// orderError est l'échec d'un ordre avec sa raison, que l'interface traduit.
type orderError struct {
	reason string
	cause  error
}

func (e *orderError) Error() string {
	return e.reason + ": " + e.cause.Error()
}

func (e *orderError) Unwrap() error {
	return e.cause
}

func failWith(reason string, cause error) error {
	return &orderError{reason: reason, cause: cause}
}

// resultOf rend ce que le serveur lira d'un ordre fini : la sortie si tout
// va bien, la raison de l'échec sinon. Ce qui n'a pas de raison à lui est
// dit path_unwritable : c'est l'écriture qui a manqué.
func resultOf(runID string, output []agentwire.OutputLine, err error) agentwire.ActionResult {
	if err == nil {
		return agentwire.ActionResult{RunID: runID, State: agentwire.ActionSucceeded, Output: output}
	}
	reason := agentwire.ReasonPathUnwritable
	var failed *orderError
	if errors.As(err, &failed) {
		reason = failed.reason
	}
	return agentwire.ActionResult{RunID: runID, State: agentwire.ActionFailed, Reason: reason, Output: output}
}

func stdout(line string) agentwire.OutputLine {
	return agentwire.OutputLine{Stream: "stdout", Line: line}
}

// coveringPath rend le chemin du droit qui couvre un dossier demandé, faux
// s'il n'y en a pas.
type coveringPath func(path string) (string, bool)

// readBackupReceive lit les paramètres de backup.receive et refuse ce que le
// droit de l'agent ne couvre pas, avant d'accepter l'ordre : un refus
// immédiat, pas un échec plus tard. Il rend aussi le chemin du droit, où
// l'écriture ouvrira sa racine.
func readBackupReceive(body agentwire.OrderBody, cover coveringPath) (agentwire.BackupReceiveParams, string, *agentwire.ActionResult) {
	var params agentwire.BackupReceiveParams
	if err := json.Unmarshal(body.Params, &params); err != nil || !isReceivable(params) {
		return params, "", &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	granted, covered := cover(params.Path)
	if !covered {
		return params, "", &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonPathOutsideRight}
	}
	return params, granted, nil
}

func readBackupPrune(body agentwire.OrderBody, cover coveringPath) (agentwire.BackupPruneParams, string, *agentwire.ActionResult) {
	var params agentwire.BackupPruneParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Keep < 1 || params.Keep > 365 {
		return params, "", &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	granted, covered := cover(params.Path)
	if !covered {
		return params, "", &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonPathOutsideRight}
	}
	return params, granted, nil
}

// openDestination ouvre le dossier de destination sous le chemin du droit.
// Le chemin du droit est celui que l'opérateur a écrit dans le fichier de
// l'agent : il est créé s'il manque (create). Dessous, aucun lien symbolique
// n'est suivi : un lien sur le chemin est refusé (path_outside_right), et la
// racine ouverte au chemin du droit refuse de toute façon ce qui sortirait
// d'elle, même posé entre la vérification et l'écriture.
func openDestination(granted, destination string, create bool) (*os.Root, error) {
	if create {
		if err := os.MkdirAll(granted, backupDirMode); err != nil { // #nosec G304 G703 -- le chemin du droit backup.receive, lu dans le fichier de l'agent, jamais celui de l'ordre
			return nil, failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	base, err := os.OpenRoot(granted)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	below := strings.TrimPrefix(strings.TrimPrefix(destination, granted), "/")
	if below == "" {
		return base, nil
	}
	defer base.Close()
	if err := refuseLinks(base, below); err != nil {
		return nil, err
	}
	if create {
		if err := base.MkdirAll(below, backupDirMode); err != nil {
			return nil, failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	root, err := base.OpenRoot(below)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return root, nil
}

// refuseLinks parcourt le chemin sous le droit, dossier par dossier : un lien
// symbolique y est refusé, ce qui n'existe pas encore sera créé.
func refuseLinks(base *os.Root, below string) error {
	prefix := ""
	for _, part := range strings.Split(below, "/") {
		prefix = path.Join(prefix, part)
		info, err := base.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return failWith(agentwire.ReasonPathUnwritable, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return failWith(agentwire.ReasonPathOutsideRight, fmt.Errorf("%s is a symbolic link", prefix))
		}
		if !info.IsDir() {
			return failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("%s is not a directory", prefix))
		}
	}
	return nil
}

// isReceivable : un colis de sauvegarde ne porte que des fichiers des deux
// formes que l'agent écrit, sans doublon, avec une taille et une somme.
func isReceivable(params agentwire.BackupReceiveParams) bool {
	if params.Parcel == "" || len(params.Files) == 0 || len(params.Files) > maxParcelFiles {
		return false
	}
	seen := map[string]bool{}
	for _, file := range params.Files {
		isKnownName := agentwire.IsBackupFile(file.Name) || agentwire.IsBackupKeyFile(file.Name)
		if !isKnownName || seen[file.Name] || file.Size < 0 || !isSHA256(file.SHA256) {
			return false
		}
		seen[file.Name] = true
	}
	return true
}

func isSHA256(text string) bool {
	decoded, err := hex.DecodeString(text)
	return err == nil && len(decoded) == sha256.Size && text == hex.EncodeToString(decoded)
}

// receiveBackup écrit sous son chemin les fichiers du colis : le dossier est
// créé s'il manque, chaque fichier est téléchargé dans un .part, vérifié
// contre la taille et la somme que l'ordre a signées, écrit sur le disque,
// renommé, puis relu depuis le disque. Il ne lit rien d'autre et ne renvoie
// aucun contenu : seulement une ligne par fichier.
func receiveBackup(ctx context.Context, source parcelSource, granted string, params agentwire.BackupReceiveParams) ([]agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, receiveTimeout)
	defer cancel()
	// Une écriture ne sort jamais du chemin du droit : le Root refuse « .. »
	// et les liens qui pointent dehors.
	root, err := openDestination(granted, params.Path, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var output []agentwire.OutputLine
	for _, file := range params.Files {
		if err := receiveFile(ctx, source, root, params.Parcel, file); err != nil {
			return output, err
		}
		output = append(output, stdout(fmt.Sprintf("written %s %d sha256:%s", file.Name, file.Size, file.SHA256)))
	}
	return output, nil
}

func receiveFile(ctx context.Context, source parcelSource, root *os.Root, parcel string, file agentwire.ParcelFile) error {
	part := partPrefix + file.Name + partSuffix
	if err := downloadTo(ctx, source, root, parcel, file, part); err != nil {
		_ = root.Remove(part)
		return err
	}
	if agentwire.IsBackupFile(file.Name) || agentwire.IsBackupKeyFile(file.Name) {
		if err := checkAgeHeader(root, part); err != nil {
			_ = root.Remove(part)
			return err
		}
	}
	if err := root.Rename(part, file.Name); err != nil {
		_ = root.Remove(part)
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	if err := fsx.SyncDir(root, "."); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	return verifyOnDisk(root, file)
}

// downloadTo écrit le fichier dans part, en s'arrêtant un octet après la
// taille signée pour voir un dépassement, et compare taille et somme.
func downloadTo(ctx context.Context, source parcelSource, root *os.Root, parcel string, file agentwire.ParcelFile, part string) error {
	body, _, err := source.open(ctx, parcel, file.Name)
	if err != nil {
		return failWith(agentwire.ReasonDownloadFailed, err)
	}
	defer body.Close()
	// Un .part laissé par une coupure part d'abord : le fichier se crée neuf,
	// jamais à travers ce qui porterait déjà son nom.
	if err := root.Remove(part); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	target, err := root.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, backupFileMode)
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := target.Chmod(backupFileMode); err != nil {
		_ = target.Close()
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	hasher := sha256.New()
	received, copyErr := io.CopyN(io.MultiWriter(target, hasher), body, file.Size+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		_ = target.Close()
		return failWith(agentwire.ReasonDownloadFailed, copyErr)
	}
	if err := target.Sync(); err != nil {
		_ = target.Close()
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := target.Close(); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if received != file.Size || hex.EncodeToString(hasher.Sum(nil)) != file.SHA256 {
		return failWith(agentwire.ReasonDigestMismatch, fmt.Errorf("%s: got %d bytes, want %d", file.Name, received, file.Size))
	}
	return nil
}

// checkAgeHeader : un fichier de sauvegarde est du format age. L'agent n'a
// pas la clé et ne le déchiffre jamais ; l'en-tête dit seulement que ce
// n'est pas autre chose.
func checkAgeHeader(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer file.Close()
	header := make([]byte, len(ageHeader))
	if _, err := io.ReadFull(file, header); err != nil || string(header) != ageHeader {
		return failWith(agentwire.ReasonNotAgeFile, fmt.Errorf("%s has no age header", name))
	}
	return nil
}

// verifyOnDisk relit le fichier écrit : taille, somme et en-tête sont ceux de
// ce que le disque garde, pas de ce que l'agent croit avoir écrit.
func verifyOnDisk(root *os.Root, file agentwire.ParcelFile) error {
	written, err := root.Open(file.Name)
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer written.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, written)
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if size != file.Size || hex.EncodeToString(hasher.Sum(nil)) != file.SHA256 {
		return failWith(agentwire.ReasonDigestMismatch, fmt.Errorf("%s changed on disk", file.Name))
	}
	return checkAgeHeader(root, file.Name)
}

// pruneBackups garde les params.Keep sauvegardes les plus récentes du dossier
// et efface les autres. Il ne touche qu'aux fichiers de la forme exacte
// d'une sauvegarde : ni une clé déposée, ni un .part, ni un fichier de
// l'opérateur. Le nom porte l'instant : le tri par nom est le tri par date.
func pruneBackups(granted string, params agentwire.BackupPruneParams) ([]agentwire.OutputLine, error) {
	root, err := openDestination(granted, params.Path, false)
	if errors.Is(err, fs.ErrNotExist) {
		return []agentwire.OutputLine{stdout("kept 0")}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	var backups []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && agentwire.IsBackupFile(entry.Name()) {
			backups = append(backups, entry.Name())
		}
	}
	slices.Sort(backups)
	surplus := max(len(backups)-params.Keep, 0)
	var output []agentwire.OutputLine
	for index, name := range backups[:surplus] {
		if err := root.Remove(name); err != nil {
			return output, failWith(agentwire.ReasonPathUnwritable, err)
		}
		if index < maxPrunedListed {
			output = append(output, stdout("removed "+name))
		}
	}
	if surplus > maxPrunedListed {
		output = append(output, stdout("removed "+strconv.Itoa(surplus-maxPrunedListed)+" more"))
	}
	if surplus > 0 {
		if err := fsx.SyncDir(root, "."); err != nil {
			return output, failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	return append(output, stdout("kept "+strconv.Itoa(len(backups)-surplus))), nil
}
