package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// controlSocketMode : seul le compte du service, ou root, s'y connecte.
const controlSocketMode = 0o600

// startControlSocket sert state_dir/control.sock : c'est par là que la
// commande locale parle à serve, sans jeton ni port. Si la socket ne peut pas
// s'ouvrir (chemin de plus de 107 octets, dossier en lecture seule, dossier
// ouvert aux autres, un autre serve déjà là), serve continue sans elle :
// observer passe avant, l'export seul en pâtit.
//
// Ce qui protège la socket : le dossier d'état, vérifié fermé au groupe et aux
// autres avant d'écouter (la socket naît avec les droits du umask, le dossier
// tient l'instant avant le chmod), puis son propre mode 0600.
func startControlSocket(logger *slog.Logger, stateDir *os.Root, statePath string, handler http.Handler) *http.Server {
	path := filepath.Join(statePath, brand.ControlSocketFile)
	if err := requirePrivateDir(stateDir); err != nil {
		logger.Warn("state_dir is open to the group or others: control socket not opened, export disabled", "path", statePath, "error", err)
		return nil
	}
	if err := removeStaleSocket(stateDir, path); err != nil {
		logger.Warn("control socket unavailable, export disabled", "path", path, "error", err)
		return nil
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		logger.Warn("control socket unavailable, export disabled", "path", path, "error", err)
		return nil
	}
	if err := stateDir.Chmod(brand.ControlSocketFile, controlSocketMode); err != nil {
		_ = listener.Close()
		logger.Warn("control socket unavailable, export disabled", "path", path, "error", err)
		return nil
	}
	control := newHTTPServer("", handler)
	go func() {
		if err := control.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("control socket stopped", "path", path, "error", err)
		}
	}()
	logger.Info("control socket", "path", path)
	return control
}

// requirePrivateDir refuse un dossier d'état que d'autres comptes peuvent
// traverser ou lire : la socket y serait atteignable pendant l'instant qui
// sépare sa création de son chmod.
func requirePrivateDir(stateDir *os.Root) error {
	info, err := stateDir.Stat(".")
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mode %04o", info.Mode().Perm())
	}
	return nil
}

// removeStaleSocket retire la socket qu'un arrêt brutal a laissée, et elle
// seule. Une socket qui répond est celle d'un autre serve vivant : la retirer
// le laisserait sans commande, donc on n'y touche pas et on n'en ouvre pas.
// Tout autre fichier du même nom reste où il est : ce n'est pas à serve de le
// jeter.
func removeStaleSocket(stateDir *os.Root, path string) error {
	info, err := stateDir.Lstat(brand.ControlSocketFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSocket == 0 {
		return errors.New("a file that is not a socket sits at its place")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = conn.Close()
		return errors.New("another server already listens on it")
	}
	// Seule une socket sans écouteur est une orpheline ; toute autre erreur
	// (droits, délai) ne prouve pas qu'elle l'est.
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot tell whether it is stale: %w", err)
	}
	return stateDir.Remove(brand.ControlSocketFile)
}
