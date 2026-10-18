package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	// L'hôte de l'URL ne sert à rien : la connexion va à la socket.
	controlHost = "control"
	// Un export est lu dans la base et écrit d'un trait : trente secondes
	// disent qu'il y a un autre souci.
	controlTimeout = 30 * time.Second
	// Ce qu'on lit d'une réponse : le fichier d'un annuaire, et de la marge.
	maxControlResponse = 4 << 20
)

// controlClient parle au serveur qui tourne par state_dir/control.sock : les
// commandes du fichier de l'annuaire passent par lui, pour que les agents
// reçoivent leurs sondes et que le direct se mette à jour.
type controlClient struct {
	statePath string
	socket    string
	http      *http.Client
}

func newControlClient(statePath string) *controlClient {
	socket := filepath.Join(statePath, brand.ControlSocketFile)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	return &controlClient{statePath: statePath, socket: socket, http: &http.Client{Transport: transport, Timeout: controlTimeout}}
}

// get rend le corps d'une réponse 200. Un serveur absent, une socket fermée
// à ce compte, un refus du serveur : chacun a son message.
func (c *controlClient) get(ctx context.Context, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+controlHost+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, c.explainDial(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxControlResponse))
	if err != nil {
		return nil, fmt.Errorf("read the server's answer: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, serverRefusal(response.StatusCode, body)
	}
	return body, nil
}

// explainDial dit ce que l'opérateur peut faire d'une socket qui ne répond
// pas : le serveur ne tourne pas, ou ce compte n'a pas le droit d'y entrer.
func (c *controlClient) explainDial(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("le serveur ne tourne pas sous %s (pas de socket %s qui réponde) : la commande passe par lui, pour que les agents reçoivent leurs sondes. Démarrez %s serve, ou vérifiez state_dir dans le fichier de configuration",
			c.statePath, brand.ControlSocketFile, brand.Command)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("ce compte n'a pas le droit d'ouvrir %s : lancez la commande sous le compte du service, ou en root", c.socket)
	}
	return fmt.Errorf("talk to the server through %s: %w", c.socket, err)
}

// serverRefusal traduit un refus du serveur en une phrase. Le corps d'une
// erreur de l'API est {"error": "<clé>"} : la clé se lit telle quelle.
func serverRefusal(status int, body []byte) error {
	var refused struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &refused) == nil && refused.Error != "" {
		return fmt.Errorf("le serveur a répondu %d : %s (voir son journal)", status, refused.Error)
	}
	return fmt.Errorf("le serveur a répondu %d", status)
}
