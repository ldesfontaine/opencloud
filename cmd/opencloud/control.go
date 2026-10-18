package main

import (
	"bytes"
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
	// Un apply écrit objet par objet, chacun par le geste de l'interface : le
	// serveur lui donne deux minutes, la commande lui en laisse une de plus.
	applyTimeout = 3 * time.Minute
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
	return &controlClient{statePath: statePath, socket: socket, http: &http.Client{Transport: transport}}
}

// get rend le corps d'une réponse 200. Un serveur absent, une socket fermée
// à ce compte, un refus du serveur : chacun a son message.
func (c *controlClient) get(ctx context.Context, path string) ([]byte, error) {
	status, body, err := c.do(ctx, http.MethodGet, path, nil, controlTimeout)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, serverRefusal(status, body)
	}
	return body, nil
}

// post envoie un corps JSON et rend le statut et le corps de la réponse, quel
// que soit le statut : un 409 ou un 422 y portent le plan d'aujourd'hui, que
// l'appelant lit.
func (c *controlClient) post(ctx context.Context, path string, payload any, timeout time.Duration) (int, []byte, error) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	// Un fichier YAML a des « < » et des « & » : pas la peine de les écrire
	// en \u003c, le serveur les relit pareil.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return 0, nil, fmt.Errorf("encode the request: %w", err)
	}
	return c.do(ctx, http.MethodPost, path, &body, timeout)
}

func (c *controlClient) do(ctx context.Context, method, path string, body io.Reader, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, "http://"+controlHost+path, body)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, c.explainDial(err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxControlResponse))
	if err != nil {
		return 0, nil, fmt.Errorf("read the server's answer: %w", err)
	}
	return response.StatusCode, answer, nil
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
