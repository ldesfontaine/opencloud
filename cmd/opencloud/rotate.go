package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// rotateSecrets change la clé de chiffrement sans rien perdre :
//  1. secret.key reçoit la neuve devant l'ancienne ; le trousseau ouvre avec
//     les deux et scelle avec la neuve ;
//  2. chaque secret est rescellé, en une transaction ;
//  3. l'ancienne est mise de côté dans secret.key.rotated-<date>, puis
//     secret.key ne garde que la neuve.
//
// Interrompue, la commande se relance : un fichier à deux clés reprend à
// l'étape 2 sans en tirer une troisième. Le serveur doit être arrêté : il
// scellerait encore avec l'ancienne.
func rotateSecrets(ctx context.Context, stateDir *os.Root, db *store.DB, out io.Writer) error {
	if err := refuseWhileServing(stateDir); err != nil {
		return err
	}
	keyring, err := loadKey(stateDir)
	if errors.Is(err, seal.ErrNoKey) {
		return &refusal{message: fmt.Sprintf("%s absent : rien à faire tourner (démarrez serve une fois, ou restaurez la clé)", brand.SecretKeyFile)}
	}
	if err != nil {
		return err
	}
	db.SetSealer(keyring)
	if err := verifySecrets(ctx, db); err != nil {
		return err
	}
	if keyring.Len() == 1 {
		keyring = keyring.WithNewKey()
		if err := seal.Save(stateDir, keyring); err != nil {
			return err
		}
		db.SetSealer(keyring)
	}
	resealed, err := db.ResealSecrets(ctx)
	if err != nil {
		return fmt.Errorf("reseal secrets: %w", err)
	}
	keptAs := brand.SecretKeyFile + ".rotated-" + time.Now().UTC().Format("20060102T150405Z")
	if err := seal.SaveAs(stateDir, keptAs, keyring.Retired()); err != nil {
		return err
	}
	current := keyring.Current()
	if err := seal.Save(stateDir, current); err != nil {
		return err
	}
	db.SetSealer(current)
	if err := verifySecrets(ctx, db); err != nil {
		return err
	}
	commandJournal(db).Record(ctx, audit.Event{Action: audit.SecretsRotate, Details: audit.Details{
		"key_id": current.ID(), "resealed": strconv.Itoa(resealed),
	}})
	_, err = fmt.Fprintf(out, "Nouvelle clé : %s (identifiant %s)\nSecrets rescellés : %d\n"+
		"Ancienne clé gardée : %s (elle sert à relire une sauvegarde de la base antérieure à ce jour ; sauvegardez la neuve avec la base)\n",
		filepath.Join(stateDir.Name(), brand.SecretKeyFile), current.ID(), resealed, filepath.Join(stateDir.Name(), keptAs))
	return err
}

// refuseWhileServing refuse quand un serveur répond sur la socket de
// contrôle. Un serveur lancé sans elle ne se voit pas : l'aide dit de
// l'arrêter avant.
func refuseWhileServing(stateDir *os.Root) error {
	conn, err := net.DialTimeout("unix", filepath.Join(stateDir.Name(), brand.ControlSocketFile), time.Second)
	if err != nil {
		return nil
	}
	_ = conn.Close()
	return &refusal{message: "un serveur tourne sur ce state_dir : arrêtez-le d'abord, rien n'a été changé"}
}

// rotateServerKey tire une clé neuve pour signer les ordres. La précédente
// reste dans server.key : au redémarrage, le serveur signe pour chaque agent
// avec la clé qu'il a épinglée, et livre la neuve à ceux restés sur la
// précédente, par un ordre que celle-ci signe.
func rotateServerKey(ctx context.Context, stateDir *os.Root, db *store.DB, out io.Writer) error {
	key, err := loadServerKey(stateDir)
	if err != nil {
		return err
	}
	rotated, err := key.Rotated()
	if errors.Is(err, action.ErrRotationPending) {
		return &refusal{message: fmt.Sprintf("une rotation est déjà en cours (clé précédente %s) : finissez-la avec `%s secrets finish-server-key`", key.PreviousFingerprint(), brand.Command)}
	}
	if err != nil {
		return err
	}
	if err := action.SaveKey(stateDir, rotated); err != nil {
		return err
	}
	commandJournal(db).Record(ctx, audit.Event{Action: audit.SecretsRotateServerKey, Details: audit.Details{
		"fingerprint": rotated.Fingerprint(), "previous_fingerprint": rotated.PreviousFingerprint(),
	}})
	_, err = fmt.Fprintf(out, "Nouvelle clé du serveur : %s\nClé précédente, encore valable : %s\n"+
		"Redémarrez le serveur : chaque agent reçoit la neuve à sa prochaine connexion. Puis lancez `%s secrets finish-server-key`.\n",
		rotated.Fingerprint(), rotated.PreviousFingerprint(), brand.Command)
	return err
}

// finishServerKey retire la clé précédente. Un agent actif qui ne l'a pas
// quittée ne pourrait plus vérifier aucun ordre : la commande refuse et le
// nomme, sauf -force.
func finishServerKey(ctx context.Context, stateDir *os.Root, db *store.DB, force bool, out io.Writer) error {
	key, err := loadServerKey(stateDir)
	if err != nil {
		return err
	}
	finished, err := key.Finished()
	if errors.Is(err, action.ErrNoRotation) {
		return &refusal{message: "aucune rotation de la clé du serveur en cours : rien à finir"}
	}
	if err != nil {
		return err
	}
	behind, err := machinesOnServerKey(ctx, db, key.PreviousFingerprint())
	if err != nil {
		return err
	}
	if len(behind) > 0 && !force {
		return &refusal{message: fmt.Sprintf("ces machines ont encore la clé précédente : %s. Attendez leur connexion, ou relancez avec -force (elles seront à ré-enrôler)", strings.Join(behind, ", "))}
	}
	if err := action.SaveKey(stateDir, finished); err != nil {
		return err
	}
	commandJournal(db).Record(ctx, audit.Event{Action: audit.SecretsFinishServerKey, Details: audit.Details{
		"fingerprint": finished.Fingerprint(), "retired_fingerprint": key.PreviousFingerprint(), "machines_left": strconv.Itoa(len(behind)),
	}})
	if _, err := fmt.Fprintf(out, "Clé précédente retirée : %s\nRedémarrez le serveur.\n", key.PreviousFingerprint()); err != nil {
		return err
	}
	if len(behind) > 0 {
		_, err = fmt.Fprintf(out, "À ré-enrôler : %s\n", strings.Join(behind, ", "))
	}
	return err
}

func loadServerKey(stateDir *os.Root) (*action.Key, error) {
	key, err := action.LoadKey(stateDir)
	switch {
	case errors.Is(err, action.ErrNoKey):
		return nil, &refusal{message: fmt.Sprintf("%s absent : démarrez serve une fois, il la crée", brand.ServerKeyFile)}
	case errors.Is(err, action.ErrKeyFileMode), errors.Is(err, action.ErrKeyFileInvalid):
		return nil, &refusal{message: fmt.Sprintf("%s illisible (%v) : restaurez la clé sauvegardée avec la base", brand.ServerKeyFile, err)}
	}
	return key, err
}

// machinesOnServerKey nomme les machines dont l'agent actif a épinglé cette
// clé.
func machinesOnServerKey(ctx context.Context, db *store.DB, fingerprint string) ([]string, error) {
	machines, err := db.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, host := range machines {
		agent := host.Agent
		if agent.Exists() && agent.RevokedAt.IsZero() && agent.ServerKeyFingerprint == fingerprint {
			names = append(names, host.Name)
		}
	}
	return names, nil
}
