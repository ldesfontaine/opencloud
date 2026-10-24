package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// snapshotName est le nom, dans le colis, de la copie de la base en clair :
// elle n'y reste que le temps d'être emballée.
const snapshotName = "snapshot.db"

// ErrNotWired : le composant est construit sans de quoi envoyer (une
// commande hors ligne) ; une sauvegarde ne part pas d'ici.
var ErrNotWired = errors.New("backup: nothing wired to send an order")

// RunNow lance la sauvegarde du plan tout de suite. Un refus avant l'ordre se
// rend à l'appelant et s'écrit aussi dans le plan, pour que la page le montre.
func (s *Service) RunNow(ctx context.Context, subject string) (action.Run, error) {
	return s.run(ctx, subject)
}

// run prépare l'archive chiffrée du plan, l'envoie à l'agent de la
// destination, puis lui demande d'élaguer. L'ordre backup.receive rendu est
// celui dont l'état dit si la sauvegarde est arrivée. Un plan à la fois :
// la nuit lance les siens l'un après l'autre.
func (s *Service) run(ctx context.Context, subject string) (action.Run, error) {
	if s.sender == nil || s.parcels == nil {
		return action.Run{}, ErrNotWired
	}
	s.running.Lock()
	defer s.running.Unlock()
	plan, err := s.store.GetBackupPlan(ctx, subject)
	if errors.Is(err, ErrNotFound) {
		return action.Run{}, ErrPlanMissing
	}
	if err != nil {
		return action.Run{}, fmt.Errorf("read backup plan: %w", err)
	}
	at := s.now()
	run, err := s.send(ctx, plan, at)
	if err != nil {
		s.noteFailure(ctx, plan.Subject, at, err)
		return action.Run{}, err
	}
	if err := s.store.NoteBackupAttempt(ctx, plan.Subject, at, "", run.ID); err != nil {
		return run, fmt.Errorf("note backup attempt: %w", err)
	}
	s.logger.Info("backup sent", "plan_id", plan.ID, "subject", plan.Subject, "run_id", run.ID, "destination_id", plan.DestinationID)
	s.auditor.Record(ctx, audit.Event{
		Action: audit.BackupPlanRun, ObjectID: plan.ID, ObjectName: plan.Subject,
		Details: audit.Details{"run_id": run.ID, "destination_id": plan.DestinationID},
	})
	s.changed()
	return run, nil
}

// noteFailure écrit dans le plan pourquoi la tentative n'est pas partie : la
// clé du refus, ou celle d'une préparation qui a échoué. Elle n'est pas
// rejouée avant la nuit suivante ou le bouton.
func (s *Service) noteFailure(ctx context.Context, subject string, at time.Time, cause error) {
	key, isRefusal := refusal.KeyOf(cause)
	if !isRefusal {
		s.logger.Error("backup not prepared", "subject", subject, "error", cause)
		key = PrepareFailedKey
	}
	if err := s.store.NoteBackupAttempt(ctx, subject, at, key, ""); err != nil {
		s.logger.Error("note backup attempt", "error", err)
	}
	s.changed()
}

// send juge ce qu'il faut pour partir (source, clé, destination, machine,
// droit), prépare le colis, puis envoie les deux ordres. Un colis dont
// l'ordre n'est pas parti est jeté.
func (s *Service) send(ctx context.Context, plan Plan, at time.Time) (action.Run, error) {
	// La source agent viendra avec l'ordre qui la produit : le plan existe,
	// la page le montre, rien ne part.
	if plan.SourceKind != SourceDatabase {
		return action.Run{}, ErrSourceNotImplemented
	}
	keyring, err := s.store.GetBackupKeyring(ctx)
	if errors.Is(err, ErrNotFound) {
		return action.Run{}, ErrKeyringMissing
	}
	if err != nil {
		return action.Run{}, fmt.Errorf("read backup key: %w", err)
	}
	destination, err := s.store.GetBackupDestination(ctx, plan.DestinationID)
	if err != nil {
		return action.Run{}, fmt.Errorf("read destination: %w", err)
	}
	target, err := s.machines.Get(ctx, destination.MachineID)
	if err != nil {
		return action.Run{}, fmt.Errorf("get machine: %w", err)
	}
	if !target.Online {
		return action.Run{}, ErrMachineOffline
	}
	if right, _ := rightOf(target, destination.Path); right == RightMissing {
		return action.Run{}, ErrRightMissing
	}

	writer, err := s.parcels.Create(destination.MachineID)
	if err != nil {
		return action.Run{}, fmt.Errorf("create parcel: %w", err)
	}
	if err := s.stage(ctx, writer, keyring, plan.Subject, at); err != nil {
		writer.Discard()
		return action.Run{}, err
	}
	parcelID, files := writer.Seal()
	receive, err := s.sendReceive(ctx, destination, parcelID, files)
	if err != nil || receive.State.IsFinished() {
		// L'ordre n'est pas parti : personne ne viendra chercher le colis.
		writer.Discard()
		return receive, err
	}
	s.sendPrune(ctx, destination, plan)
	return receive, nil
}

func (s *Service) sendReceive(ctx context.Context, destination Destination, parcelID string, files []agentwire.ParcelFile) (action.Run, error) {
	params, err := json.Marshal(agentwire.BackupReceiveParams{Parcel: parcelID, Path: destination.Path, Files: files})
	if err != nil {
		return action.Run{}, fmt.Errorf("encode receive params: %w", err)
	}
	return s.sender.Send(ctx, destination.MachineID, agentwire.KindBackupReceive, params)
}

// sendPrune demande à l'agent de ne garder que les plus récentes du plan,
// sous son préfixe : les fichiers des autres plans du même dossier ne sont
// pas à lui. L'agent exécute ses ordres longs l'un après l'autre : l'élagage
// passe après la réception. S'il ne part pas, la sauvegarde est faite quand
// même, et le prochain passage élaguera.
func (s *Service) sendPrune(ctx context.Context, destination Destination, plan Plan) {
	params, err := json.Marshal(agentwire.BackupPruneParams{Path: destination.Path, Prefix: plan.FilePrefix(), Keep: plan.Keep})
	if err != nil {
		s.logger.Error("encode prune params", "error", err)
		return
	}
	if _, err := s.sender.Send(ctx, destination.MachineID, agentwire.KindBackupPrune, params); err != nil {
		s.logger.Warn("backup prune not sent", "destination_id", destination.ID, "error", err)
	}
}

// stage écrit dans le colis l'archive chiffrée et la clé scellée. La copie
// de la base en clair n'existe que le temps d'être emballée, dans le dossier
// du colis (0700, même disque que la base) : elle est effacée avant l'envoi.
func (s *Service) stage(ctx context.Context, writer *action.ParcelWriter, keyring Keyring, subject string, at time.Time) error {
	recipient, err := age.ParseX25519Recipient(keyring.Recipient)
	if err != nil {
		return fmt.Errorf("parse backup recipient: %w", err)
	}
	// Le fichier de l'instantané naît vide en 0600 (VACUUM INTO accepte un
	// fichier vide) : SQLite le créerait sinon en 0644, umask compris.
	placeholder, err := writer.Create(snapshotName)
	if err != nil {
		return err
	}
	if err := placeholder.Close(); err != nil {
		return err
	}
	if err := s.store.SnapshotDatabase(ctx, writer.Path(snapshotName)); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	archive, err := writer.Create(agentwire.BackupFileName(subject, at))
	if err != nil {
		return err
	}
	packErr := writeArchive(archive, recipient, []archiveFile{
		{Name: brand.DatabaseFile, Mode: archiveFileMode, Open: func() (io.ReadCloser, int64, error) { return openStaged(writer, snapshotName) }},
		{Name: brand.SecretKeyFile, Mode: archiveFileMode, Open: func() (io.ReadCloser, int64, error) { return openInState(s.stateDir, brand.SecretKeyFile) }},
		{Name: brand.ServerKeyFile, Mode: archiveFileMode, Open: func() (io.ReadCloser, int64, error) { return openInState(s.stateDir, brand.ServerKeyFile) }},
	})
	if err := errors.Join(packErr, archive.Close()); err != nil {
		return fmt.Errorf("pack backup: %w", err)
	}
	if err := writer.Remove(snapshotName); err != nil {
		return fmt.Errorf("remove clear snapshot: %w", err)
	}
	return s.stageSealedKey(writer, keyring)
}

// stageSealedKey dépose la clé privée scellée à côté de la sauvegarde, sous
// un nom par empreinte : remplacer la clé plus tard ne rend rien illisible.
func (s *Service) stageSealedKey(writer *action.ParcelWriter, keyring Keyring) error {
	key, err := writer.Create(agentwire.BackupKeyFileName(keyring.Fingerprint))
	if err != nil {
		return err
	}
	_, writeErr := key.Write(keyring.IdentitySealed)
	if err := errors.Join(writeErr, key.Close()); err != nil {
		return fmt.Errorf("write sealed key: %w", err)
	}
	return nil
}

func openStaged(writer *action.ParcelWriter, name string) (io.ReadCloser, int64, error) {
	file, size, err := writer.Open(name)
	if err != nil {
		return nil, 0, err
	}
	return file, size, nil
}

func openInState(stateDir *os.Root, name string) (io.ReadCloser, int64, error) {
	file, err := stateDir.Open(name)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}
