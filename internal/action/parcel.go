package action

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// ParcelLifetime est le temps qu'un colis reste sur la machine openCloud
// avant de partir, téléchargé ou non : aucune sauvegarde ne s'y garde. Il ne
// survit pas à son ordre : un ordre non pris expire après OrderLifetime, un
// ordre pris échoue MaxRunDuration après sa demande (timeout), et l'agent ne
// reprend pas un téléchargement.
const ParcelLifetime = OrderLifetime + MaxRunDuration

const (
	parcelDirMode  = 0o700
	parcelFileMode = 0o600
)

// ErrParcelNotFound : le colis est inconnu, échu, d'une autre machine, ou
// n'a pas ce fichier. Les quatre se disent pareil à celui qui demande.
var ErrParcelNotFound = errors.New("parcel not found")

// ErrParcelName : le nom d'un fichier de colis est un nom simple.
var ErrParcelName = errors.New("parcel file name is not a plain name")

var parcelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Outbox tient les colis que les agents téléchargent : un ordre signé ne
// porte que leur identifiant, leurs tailles et leurs sommes. Un événement du
// flux est borné à 1 Mio ; une base fait des dizaines de Mio. Les colis
// vivent sous <état>/outgoing/<colis>/ (0700, même disque et mêmes droits que
// la base), pour ParcelLifetime au plus, et le dossier est vidé au démarrage.
type Outbox struct {
	root *os.Root
	now  func() time.Time

	mu      sync.Mutex
	parcels map[string]*parcel
}

// parcel est un colis scellé : pour une seule machine, jusqu'à expiresAt.
type parcel struct {
	machineID string
	expiresAt time.Time
	files     map[string]parcelEntry
}

// parcelEntry : où lire un fichier, sous la racine de l'état — dans le
// dossier du colis, ou ailleurs pour un fichier emprunté.
type parcelEntry struct {
	path string
}

// NewOutbox vide ce qu'un arrêt a laissé dans outgoing/ et le recrée vide :
// un colis ne survit pas à un redémarrage.
func NewOutbox(stateDir *os.Root) (*Outbox, error) {
	if err := stateDir.RemoveAll(brand.OutgoingDir); err != nil {
		return nil, fmt.Errorf("clear outgoing: %w", err)
	}
	if err := stateDir.Mkdir(brand.OutgoingDir, parcelDirMode); err != nil {
		return nil, fmt.Errorf("create outgoing: %w", err)
	}
	return &Outbox{root: stateDir, now: time.Now, parcels: map[string]*parcel{}}, nil
}

func (o *Outbox) SetClock(now func() time.Time) {
	o.now = now
}

// Create ouvre un colis pour cette machine. Rien n'est visible d'un agent
// avant Seal ; un colis abandonné se jette par Discard.
func (o *Outbox) Create(machineID string) (*ParcelWriter, error) {
	id, err := ident.New()
	if err != nil {
		return nil, fmt.Errorf("generate parcel id: %w", err)
	}
	dir := path.Join(brand.OutgoingDir, id)
	if err := o.root.Mkdir(dir, parcelDirMode); err != nil {
		return nil, fmt.Errorf("create parcel dir: %w", err)
	}
	return &ParcelWriter{outbox: o, id: id, dir: dir, machineID: machineID, files: map[string]stagedFile{}}, nil
}

// Open ouvre un fichier d'un colis pour la machine qui le demande, et rend sa
// taille. Un colis échu n'est plus servi, même avant le prochain balayage.
func (o *Outbox) Open(machineID, parcelID, name string) (*os.File, int64, error) {
	entry, found := o.find(machineID, parcelID, name)
	if !found {
		return nil, 0, ErrParcelNotFound
	}
	file, err := o.root.Open(entry.path)
	if err != nil {
		return nil, 0, ErrParcelNotFound
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat parcel file: %w", err)
	}
	return file, info.Size(), nil
}

func (o *Outbox) find(machineID, parcelID, name string) (parcelEntry, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	sealed, found := o.parcels[parcelID]
	if !found || sealed.machineID != machineID || !o.now().Before(sealed.expiresAt) {
		return parcelEntry{}, false
	}
	entry, found := sealed.files[name]
	return entry, found
}

// Sweep retire les colis échus, fichiers compris. Les fichiers empruntés à
// l'état (Borrow) ne sont pas dans le dossier du colis : ils restent.
func (o *Outbox) Sweep() {
	if o == nil {
		return
	}
	now := o.now()
	o.mu.Lock()
	var expired []string
	for id, sealed := range o.parcels {
		if !now.Before(sealed.expiresAt) {
			expired = append(expired, id)
			delete(o.parcels, id)
		}
	}
	o.mu.Unlock()
	for _, id := range expired {
		_ = o.root.RemoveAll(path.Join(brand.OutgoingDir, id))
	}
}

// ParcelWriter prépare un colis : ses fichiers s'écrivent un à un, leur
// somme et leur taille se calculent à l'écriture.
type ParcelWriter struct {
	outbox    *Outbox
	id        string
	dir       string
	machineID string
	files     map[string]stagedFile
}

type stagedFile struct {
	path     string
	size     int64
	sum      string
	borrowed bool
}

// Path rend le chemin absolu d'un fichier du colis : de quoi donner à SQLite
// un fichier où écrire (VACUUM INTO), le seul accès qui ne passe pas par le
// descripteur de l'état.
func (w *ParcelWriter) Path(name string) string {
	return filepath.Join(w.outbox.root.Name(), w.dir, name)
}

// Create ouvre un fichier neuf du colis, en 0600.
func (w *ParcelWriter) Create(name string) (io.WriteCloser, error) {
	if !parcelNamePattern.MatchString(name) {
		return nil, fmt.Errorf("%q: %w", name, ErrParcelName)
	}
	filePath := path.Join(w.dir, name)
	file, err := w.outbox.root.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, parcelFileMode)
	if err != nil {
		return nil, fmt.Errorf("create parcel file: %w", err)
	}
	return &stagedWriter{file: file, hash: sha256.New(), parcel: w, name: name, path: filePath}, nil
}

// Open relit un fichier déjà écrit du colis (l'instantané de la base, avant
// qu'il ne soit emballé).
func (w *ParcelWriter) Open(name string) (*os.File, int64, error) {
	if !parcelNamePattern.MatchString(name) {
		return nil, 0, fmt.Errorf("%q: %w", name, ErrParcelName)
	}
	file, err := w.outbox.root.Open(path.Join(w.dir, name))
	if err != nil {
		return nil, 0, fmt.Errorf("open parcel file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, fmt.Errorf("stat parcel file: %w", err)
	}
	return file, info.Size(), nil
}

// Borrow ajoute au colis un fichier qui existe déjà sous l'état (chemin
// relatif à lui) : sa somme est calculée tout de suite, il n'est jamais copié
// ni effacé par la boîte.
func (w *ParcelWriter) Borrow(name, statePath string) error {
	if !parcelNamePattern.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrParcelName)
	}
	file, err := w.outbox.root.Open(statePath)
	if err != nil {
		return fmt.Errorf("open borrowed file: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return fmt.Errorf("sum borrowed file: %w", err)
	}
	w.files[name] = stagedFile{path: statePath, size: size, sum: hex.EncodeToString(hasher.Sum(nil)), borrowed: true}
	return nil
}

// Remove efface un fichier du colis (l'instantané en clair, une fois
// emballé) et l'oublie.
func (w *ParcelWriter) Remove(name string) error {
	if !parcelNamePattern.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrParcelName)
	}
	staged, found := w.files[name]
	if found && staged.borrowed {
		delete(w.files, name)
		return nil
	}
	delete(w.files, name)
	err := w.outbox.root.Remove(path.Join(w.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Seal publie le colis pour la machine, jusqu'à ParcelLifetime, et rend son
// identifiant avec ses fichiers dans l'ordre de leur nom : c'est ce que
// l'ordre signé porte.
func (w *ParcelWriter) Seal() (string, []agentwire.ParcelFile) {
	names := make([]string, 0, len(w.files))
	for name := range w.files {
		names = append(names, name)
	}
	slices.Sort(names)
	sealed := &parcel{machineID: w.machineID, expiresAt: w.outbox.now().Add(ParcelLifetime), files: map[string]parcelEntry{}}
	listed := make([]agentwire.ParcelFile, 0, len(names))
	for _, name := range names {
		staged := w.files[name]
		sealed.files[name] = parcelEntry{path: staged.path}
		listed = append(listed, agentwire.ParcelFile{Name: name, Size: staged.size, SHA256: staged.sum})
	}
	w.outbox.mu.Lock()
	w.outbox.parcels[w.id] = sealed
	w.outbox.mu.Unlock()
	return w.id, listed
}

// Discard jette un colis qui n'est pas parti : ses fichiers sont effacés.
func (w *ParcelWriter) Discard() {
	w.outbox.mu.Lock()
	delete(w.outbox.parcels, w.id)
	w.outbox.mu.Unlock()
	_ = w.outbox.root.RemoveAll(w.dir)
}

// stagedWriter écrit un fichier du colis en comptant sa taille et sa somme.
type stagedWriter struct {
	file   *os.File
	hash   hash.Hash
	parcel *ParcelWriter
	name   string
	path   string
	size   int64
}

func (w *stagedWriter) Write(content []byte) (int, error) {
	written, err := w.file.Write(content)
	w.hash.Write(content[:written])
	w.size += int64(written)
	return written, err
}

// Close fsync le fichier puis l'inscrit au colis avec sa somme : un fichier
// pas fermé n'y figure pas.
func (w *stagedWriter) Close() error {
	syncErr := w.file.Sync()
	closeErr := w.file.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("close parcel file: %w", err)
	}
	w.parcel.files[w.name] = stagedFile{path: w.path, size: w.size, sum: hex.EncodeToString(w.hash.Sum(nil))}
	return nil
}

// OpenParcel ouvre un fichier d'un colis de cette machine : c'est ce
// qu'appelle la route de téléchargement de l'agent.
func (s *Service) OpenParcel(machineID, parcelID, name string) (*os.File, int64, error) {
	if s.outbox == nil {
		return nil, 0, ErrParcelNotFound
	}
	return s.outbox.Open(machineID, parcelID, name)
}
