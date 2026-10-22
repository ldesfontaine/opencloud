package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	archiveFileMode = 0o600
	// Les bornes d'une extraction : une base de 64 Gio est déjà hors du
	// commun, une clé tient en quelques dizaines d'octets.
	maxDatabaseBytes = 64 << 30
	maxKeyBytes      = 64 << 10
	partSuffix       = ".part"
)

var (
	// ErrUnexpectedEntry : l'archive porte un fichier hors des trois connus,
	// un doublon, ou autre chose qu'un fichier ordinaire.
	ErrUnexpectedEntry = errors.New("backup archive holds an entry it should not")
	// ErrEntryTooLarge : un fichier de l'archive dépasse sa borne.
	ErrEntryTooLarge = errors.New("backup archive entry larger than allowed")
	// ErrArchiveIncomplete : un des trois fichiers manque.
	ErrArchiveIncomplete = errors.New("backup archive misses a file")
)

// archiveNames sont les trois fichiers d'une sauvegarde de la base, dans
// l'ordre où ils s'écrivent, avec la borne de chacun. La liste est fermée :
// c'est elle qui règle, à l'extraction, la traversée de chemin (un nom hors
// d'elle est refusé avant d'être lu comme chemin).
var archiveNames = []archiveName{
	{brand.DatabaseFile, maxDatabaseBytes},
	{brand.SecretKeyFile, maxKeyBytes},
	{brand.ServerKeyFile, maxKeyBytes},
}

type archiveName struct {
	name  string
	limit int64
}

// archiveFile est un fichier à emballer : Open le donne avec sa taille.
type archiveFile struct {
	Name string
	Mode int64
	Open func() (io.ReadCloser, int64, error)
}

// writeArchive emballe les fichiers dans un tar, sans compression, chiffré
// pour la clé publique : ce qui sort de w ne se lit qu'avec la privée. Le
// tar n'est jamais écrit en clair nulle part.
func writeArchive(w io.Writer, recipient age.Recipient, files []archiveFile) error {
	encrypted, err := age.Encrypt(w, recipient)
	if err != nil {
		return fmt.Errorf("start encryption: %w", err)
	}
	packer := tar.NewWriter(encrypted)
	for _, file := range files {
		if err := addToArchive(packer, file); err != nil {
			return err
		}
	}
	if err := packer.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	if err := encrypted.Close(); err != nil {
		return fmt.Errorf("finish encryption: %w", err)
	}
	return nil
}

func addToArchive(packer *tar.Writer, file archiveFile) error {
	source, size, err := file.Open()
	if err != nil {
		return fmt.Errorf("open %s: %w", file.Name, err)
	}
	defer source.Close()
	// Ni date ni propriétaire : l'archive ne dit rien de la machine.
	header := &tar.Header{Typeflag: tar.TypeReg, Name: file.Name, Mode: file.Mode, Size: size, ModTime: time.Unix(0, 0)}
	if err := packer.WriteHeader(header); err != nil {
		return fmt.Errorf("write header %s: %w", file.Name, err)
	}
	if _, err := io.CopyN(packer, source, size); err != nil {
		return fmt.Errorf("pack %s: %w", file.Name, err)
	}
	return nil
}

// ExtractArchive déchiffre une sauvegarde et en écrit les trois fichiers
// sous target, en 0600. Rien n'apparaît sous son nom définitif tant que
// l'archive n'a pas été lue jusqu'au bout et authentifiée : une archive
// refusée au milieu ne laisse que des .part, effacés.
func ExtractArchive(source io.Reader, identity age.Identity, target *os.Root) error {
	decrypted, err := age.Decrypt(source, identity)
	if err != nil {
		return fmt.Errorf("decrypt backup: %w", err)
	}
	unpacker := tar.NewReader(decrypted)
	written := map[string]bool{}
	cleanup := func() {
		for _, entry := range archiveNames {
			_ = target.Remove(entry.name + partSuffix)
		}
	}
	for {
		header, err := unpacker.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return fmt.Errorf("read tar: %w", err)
		}
		if err := extractEntry(unpacker, header, target, written); err != nil {
			cleanup()
			return err
		}
	}
	// Le tar fini, le reste du flux age est lu : c'est lui qui prouve que
	// rien n'a été tronqué.
	if _, err := io.Copy(io.Discard, decrypted); err != nil {
		cleanup()
		return fmt.Errorf("finish decrypting backup: %w", err)
	}
	for _, entry := range archiveNames {
		if !written[entry.name] {
			cleanup()
			return fmt.Errorf("%s: %w", entry.name, ErrArchiveIncomplete)
		}
	}
	return publishExtracted(target)
}

func extractEntry(unpacker *tar.Reader, header *tar.Header, target *os.Root, written map[string]bool) error {
	limit, known := limitOf(header.Name)
	if !known || written[header.Name] || header.Typeflag != tar.TypeReg {
		return fmt.Errorf("%q: %w", header.Name, ErrUnexpectedEntry)
	}
	if header.Size < 0 || header.Size > limit {
		return fmt.Errorf("%s: %w", header.Name, ErrEntryTooLarge)
	}
	file, err := target.OpenFile(header.Name+partSuffix, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, archiveFileMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", header.Name, err)
	}
	if err := file.Chmod(archiveFileMode); err != nil {
		_ = file.Close()
		return fmt.Errorf("chmod %s: %w", header.Name, err)
	}
	if _, err := io.CopyN(file, unpacker, header.Size); err != nil {
		_ = file.Close()
		return fmt.Errorf("extract %s: %w", header.Name, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync %s: %w", header.Name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", header.Name, err)
	}
	written[header.Name] = true
	return nil
}

func limitOf(name string) (int64, bool) {
	for _, entry := range archiveNames {
		if entry.name == name {
			return entry.limit, true
		}
	}
	return 0, false
}

// publishExtracted donne leur nom définitif aux trois fichiers.
func publishExtracted(target *os.Root) error {
	for _, entry := range archiveNames {
		if err := target.Rename(entry.name+partSuffix, entry.name); err != nil {
			return fmt.Errorf("publish %s: %w", entry.name, err)
		}
	}
	return fsx.SyncDir(target, ".")
}
