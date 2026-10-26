package action

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/ident"
)

// Upload est un colis pour la machine de destination que l'agent d'une autre
// machine, la productrice, remplit par téléversement : le colis d'une
// sauvegarde produite (backup.produce), qui transite par le serveur sans que
// celui-ci puisse la lire, elle est chiffrée avant de partir.
type Upload struct {
	outbox *Outbox
	id     string
	dir    string
}

// CreateForUpload ouvre un colis pour la machine de destination, que seule la
// machine productrice remplit. Il dure ParcelLifetime comme les autres ; un
// colis abandonné se jette par Discard.
func (o *Outbox) CreateForUpload(destinationID, uploaderID string) (*Upload, error) {
	id, err := ident.New()
	if err != nil {
		return nil, fmt.Errorf("generate parcel id: %w", err)
	}
	dir := path.Join(brand.OutgoingDir, id)
	if err := o.root.Mkdir(dir, parcelDirMode); err != nil {
		return nil, fmt.Errorf("create parcel dir: %w", err)
	}
	o.mu.Lock()
	o.parcels[id] = &parcel{
		machineID: destinationID, uploader: uploaderID,
		expiresAt: o.now().Add(ParcelLifetime), files: map[string]parcelEntry{},
	}
	o.mu.Unlock()
	return &Upload{outbox: o, id: id, dir: dir}, nil
}

// ID est l'identifiant que l'ordre produce porte à l'agent producteur et que
// l'ordre receive porte à l'agent de destination.
func (u *Upload) ID() string {
	return u.id
}

// Files rend ce qui est arrivé, dans l'ordre des noms, avec la taille et la
// somme que le serveur a calculées : c'est ce que l'ordre receive signe.
func (u *Upload) Files() []agentwire.ParcelFile {
	u.outbox.mu.Lock()
	defer u.outbox.mu.Unlock()
	sealed, found := u.outbox.parcels[u.id]
	if !found {
		return nil
	}
	names := make([]string, 0, len(sealed.files))
	for name := range sealed.files {
		names = append(names, name)
	}
	slices.Sort(names)
	listed := make([]agentwire.ParcelFile, 0, len(names))
	for _, name := range names {
		entry := sealed.files[name]
		listed = append(listed, agentwire.ParcelFile{Name: name, Size: entry.size, SHA256: entry.sum})
	}
	return listed
}

// Stage dépose dans le colis un fichier que le serveur écrit lui-même, avant
// que la productrice téléverse : la clé scellée qui voyage avec la sauvegarde.
// Le fichier part avec les autres dans l'ordre receive.
func (u *Upload) Stage(name string, content []byte) error {
	if !parcelNamePattern.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrParcelName)
	}
	filePath := path.Join(u.dir, name)
	file, err := u.outbox.root.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, parcelFileMode)
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	if err := errors.Join(writeErr, syncErr, file.Close()); err != nil {
		_ = u.outbox.root.Remove(filePath)
		return fmt.Errorf("write staged file: %w", err)
	}
	sum := sha256.Sum256(content)
	u.outbox.mu.Lock()
	defer u.outbox.mu.Unlock()
	sealed, found := u.outbox.parcels[u.id]
	if !found {
		return ErrParcelNotFound
	}
	sealed.files[name] = parcelEntry{path: filePath, size: int64(len(content)), sum: hex.EncodeToString(sum[:])}
	return nil
}

// Handover remet le colis à la machine de destination et rend ses fichiers :
// la productrice n'y écrit plus, et la destination a toute la vie d'un colis
// pour le télécharger, comptée d'ici et non de la création (produire peut
// durer presque une heure).
func (u *Upload) Handover() []agentwire.ParcelFile {
	u.outbox.mu.Lock()
	sealed, found := u.outbox.parcels[u.id]
	if found {
		sealed.uploader = ""
		sealed.expiresAt = u.outbox.now().Add(ParcelLifetime)
	}
	u.outbox.mu.Unlock()
	return u.Files()
}

// Discard jette le colis et ses fichiers.
func (u *Upload) Discard() {
	u.outbox.mu.Lock()
	delete(u.outbox.parcels, u.id)
	u.outbox.mu.Unlock()
	_ = u.outbox.root.RemoveAll(u.dir)
}

// Receive écrit un fichier que la machine productrice téléverse. Le colis doit
// l'attendre, elle et nulle autre ; le nom est celui d'une archive produite,
// la taille et la somme sont celles qu'elle annonce et que le serveur
// recalcule en écrivant. Le fichier s'écrit dans un .part, renommé quand tout
// concorde : un échec ne laisse rien. Le contenu n'est jamais chargé en
// mémoire.
func (o *Outbox) Receive(uploaderID, parcelID, name string, content io.Reader, size int64, sum string) error {
	sealed, err := o.claimUpload(uploaderID, parcelID, name, size)
	if err != nil {
		return err
	}
	dir := path.Join(brand.OutgoingDir, parcelID)
	entry, err := o.writeUpload(dir, name, content, size, sum)
	o.mu.Lock()
	sealed.receiving = false
	stillThere := o.parcels[parcelID] == sealed
	if err == nil && stillThere {
		sealed.files[name] = entry
	}
	o.mu.Unlock()
	if err == nil && !stillThere {
		// Le colis a échu pendant l'écriture : son dossier part avec lui.
		_ = o.root.Remove(entry.path)
		return ErrParcelNotFound
	}
	return err
}

// claimUpload juge la demande sous le verrou et réserve le colis : un seul
// fichier en vol à la fois.
func (o *Outbox) claimUpload(uploaderID, parcelID, name string, size int64) (*parcel, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	sealed, found := o.parcels[parcelID]
	if !found || sealed.uploader == "" || sealed.uploader != uploaderID || !o.now().Before(sealed.expiresAt) {
		return nil, ErrParcelNotFound
	}
	if !agentwire.IsBackupFile(name) || !parcelNamePattern.MatchString(name+uploadPartSuffix) {
		return nil, ErrUploadName
	}
	if size < 0 || size > MaxUploadBytes {
		return nil, ErrUploadTooLarge
	}
	if _, written := sealed.files[name]; written || sealed.receiving || len(sealed.files) >= maxUploadedFiles {
		return nil, ErrUploadConflict
	}
	if !o.hasRoomFor(size) {
		return nil, ErrUploadDiskFull
	}
	sealed.receiving = true
	return sealed, nil
}

// hasRoomFor : le disque de l'état garde MinFreeAfterUpload libres une fois le
// fichier écrit. Une mesure impossible ne bloque pas : l'écriture dira.
func (o *Outbox) hasRoomFor(size int64) bool {
	free, err := o.freeBytes(o.root.Name())
	if err != nil {
		return true
	}
	return free >= uint64(size)+MinFreeAfterUpload // #nosec G115 -- size est borné par MaxUploadBytes juste avant.
}

func (o *Outbox) writeUpload(dir, name string, content io.Reader, size int64, sum string) (parcelEntry, error) {
	partPath := path.Join(dir, name+uploadPartSuffix)
	finalPath := path.Join(dir, name)
	target, err := o.root.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, parcelFileMode)
	if err != nil {
		return parcelEntry{}, fmt.Errorf("create uploaded file: %w", err)
	}
	hasher := sha256.New()
	// Un octet de plus que annoncé, pour voir un dépassement.
	received, copyErr := io.CopyN(io.MultiWriter(target, hasher), content, size+1)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		_ = target.Close()
		_ = o.root.Remove(partPath)
		return parcelEntry{}, fmt.Errorf("receive uploaded file: %w", copyErr)
	}
	syncErr := target.Sync()
	if err := errors.Join(syncErr, target.Close()); err != nil {
		_ = o.root.Remove(partPath)
		return parcelEntry{}, fmt.Errorf("write uploaded file: %w", err)
	}
	if received != size || hex.EncodeToString(hasher.Sum(nil)) != sum {
		_ = o.root.Remove(partPath)
		return parcelEntry{}, ErrUploadDigest
	}
	if err := o.root.Rename(partPath, finalPath); err != nil {
		_ = o.root.Remove(partPath)
		return parcelEntry{}, fmt.Errorf("publish uploaded file: %w", err)
	}
	return parcelEntry{path: finalPath, size: size, sum: sum}, nil
}

// freeBytesUnder rend l'espace qu'un compte sans privilège peut écrire sous ce
// dossier (Bavail, pas Bfree : la réserve de root n'en est pas).
func freeBytesUnder(dir string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, fmt.Errorf("statfs state dir: %w", err)
	}
	blockSize := uint64(stat.Bsize) // #nosec G115 -- une taille de bloc, toujours positive.
	return stat.Bavail * blockSize, nil
}
