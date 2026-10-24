package proxmox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

const (
	requestTimeout = 20 * time.Second
	// Une grappe de quelques centaines de VM tient dans bien moins ; au-delà,
	// ce n'est plus l'API qu'on croit lire.
	maxResponseBytes = 8 << 20
	apiPrefix        = "/api2/json"

	// Chaque texte que l'API rend (nom, état, nœud, stockage, type, contenu)
	// est borné avant d'entrer en base et à l'écran : Proxmox les garde bien
	// plus courts (un nom de VM est un nom DNS), un texte plus long ne dit rien
	// de plus.
	maxTextBytes = 128
	// Au-delà, ce n'est plus l'hyperviseur d'une infra qu'on lit : le relevé
	// est refusé plutôt que d'écrire des dizaines de milliers de lignes.
	maxGuests   = 5000
	maxStorages = 1000
	maxContents = 16
)

// errPinMismatch : le certificat présenté n'est pas celui dont l'opérateur a
// donné l'empreinte.
var errPinMismatch = errors.New("certificate does not match the pin")

// Client parle à l'API d'un hôte, en lecture. Un client sert un relevé : il ne
// garde aucune connexion ouverte.
type Client struct {
	http          *http.Client
	baseURL       string
	authorization string
}

// NewClient prépare le client d'un hôte. Le jeton part dans l'en-tête
// Authorization et nulle part ailleurs : ni dans l'adresse, ni dans une erreur.
func NewClient(credentials Credentials) *Client {
	transport := &http.Transport{
		// Pas de mandataire d'environnement : tout sort par la garde egress.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Le réseau local est le cas normal d'un Proxmox ; le lien-local
			// (métadonnées des hébergeurs) reste fermé.
			return egress.DialWith(ctx, network, address, requestTimeout, true)
		},
		TLSClientConfig:       tlsConfig(credentials.Pin),
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
		DisableKeepAlives:     true,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Timeout:   requestTimeout,
			// Une API qui redirige n'est pas celle qu'on a déclarée.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		baseURL:       credentials.APIURL + apiPrefix,
		authorization: "PVEAPIToken=" + credentials.TokenID + "=" + credentials.TokenSecret,
	}
}

// tlsConfig juge le certificat par le magasin du système, ou, avec une
// empreinte, par elle seule : un Proxmox livre un certificat auto-signé.
func tlsConfig(pin string) *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if pin == "" {
		return config
	}
	expected, err := hex.DecodeString(pin)
	if err != nil {
		// Definition.Normalize a déjà refusé ce qui n'est pas de l'hexadécimal.
		expected = nil
	}
	config.InsecureSkipVerify = true // #nosec G402 -- la chaîne n'est pas vérifiée, l'empreinte l'est, juste en dessous.
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errPinMismatch
		}
		return verifyPin(state.PeerCertificates[0], expected)
	}
	return config
}

func verifyPin(certificate *x509.Certificate, expected []byte) error {
	sum := sha256.Sum256(certificate.Raw)
	if !bytes.Equal(sum[:], expected) {
		return errPinMismatch
	}
	return nil
}

// envelope est la forme de toute réponse de l'API : {"data": …}.
type envelope struct {
	Data json.RawMessage `json:"data"`
}

// get lit une ressource et décode son champ data. Chaque échec devient l'un
// des quatre refus, avec sa cause dans la chaîne pour le journal.
func (c *Client) get(ctx context.Context, path string, query url.Values, into any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrBadResponse, err)
	}
	request.Header.Set("Authorization", c.authorization)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return transportRefusal(err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d", ErrUnauthorized, response.StatusCode)
	case response.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: status %d", ErrBadResponse, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return transportRefusal(err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("%w: answer over %d bytes", ErrBadResponse, maxResponseBytes)
	}
	var wrapped envelope
	if err := json.Unmarshal(body, &wrapped); err != nil || len(wrapped.Data) == 0 {
		return fmt.Errorf("%w: no data in the answer", ErrBadResponse)
	}
	if err := json.Unmarshal(wrapped.Data, into); err != nil {
		return fmt.Errorf("%w: decode data: %w", ErrBadResponse, err)
	}
	return nil
}

// transportRefusal range un échec de transport : un certificat refusé se
// distingue d'un hôte qui ne répond pas, c'est ce qui dit d'épingler.
func transportRefusal(err error) error {
	var verification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.Is(err, errPinMismatch) || errors.As(err, &verification) ||
		errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return fmt.Errorf("%w: %w", ErrCertificate, err)
	}
	return fmt.Errorf("%w: %w", ErrUnreachable, err)
}

// Version lit la version de Proxmox VE : la plus petite lecture qui exige un
// jeton valide, d'où son usage à la déclaration.
func (c *Client) Version(ctx context.Context) (string, error) {
	var version struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/version", nil, &version); err != nil {
		return "", err
	}
	return version.Version, nil
}

// resource est une ligne de /cluster/resources : une VM, un conteneur ou un
// stockage selon le type demandé.
type resource struct {
	Type       string  `json:"type"`
	VMID       int     `json:"vmid"`
	Name       string  `json:"name"`
	Node       string  `json:"node"`
	Status     string  `json:"status"`
	Template   int     `json:"template"`
	MaxCPU     float64 `json:"maxcpu"`
	MaxMem     int64   `json:"maxmem"`
	MaxDisk    int64   `json:"maxdisk"`
	Disk       int64   `json:"disk"`
	Storage    string  `json:"storage"`
	PluginType string  `json:"plugintype"`
	Content    string  `json:"content"`
}

func (c *Client) resources(ctx context.Context, kind string) ([]resource, error) {
	var listed []resource
	err := c.get(ctx, "/cluster/resources", url.Values{"type": {kind}}, &listed)
	return listed, err
}

// Guests lit les VM et les conteneurs de toute la grappe, modèles compris.
func (c *Client) Guests(ctx context.Context) ([]Guest, error) {
	listed, err := c.resources(ctx, "vm")
	if err != nil {
		return nil, err
	}
	if len(listed) > maxGuests {
		return nil, fmt.Errorf("%w: %d guests, over %d", ErrBadResponse, len(listed), maxGuests)
	}
	guests := make([]Guest, 0, len(listed))
	seen := map[int]bool{}
	for _, entry := range listed {
		// Un numéro est unique dans une grappe : un doublon ferait échouer
		// l'écriture du relevé entier, le premier vu reste.
		if (entry.Type != KindQEMU && entry.Type != KindLXC) || seen[entry.VMID] {
			continue
		}
		seen[entry.VMID] = true
		guests = append(guests, Guest{
			VMID: entry.VMID, Kind: entry.Type, Name: boundedText(entry.Name), Status: boundedText(entry.Status),
			Template: entry.Template == 1, CPUs: entry.MaxCPU, MaxMem: entry.MaxMem, MaxDisk: entry.MaxDisk,
			Node: boundedText(entry.Node),
		})
	}
	return guests, nil
}

// Storages lit les stockages de toute la grappe, un par nœud pour un stockage
// local.
func (c *Client) Storages(ctx context.Context) ([]Storage, error) {
	listed, err := c.resources(ctx, "storage")
	if err != nil {
		return nil, err
	}
	if len(listed) > maxStorages {
		return nil, fmt.Errorf("%w: %d storages, over %d", ErrBadResponse, len(listed), maxStorages)
	}
	storages := make([]Storage, 0, len(listed))
	seen := map[string]bool{}
	for _, entry := range listed {
		node, name := boundedText(entry.Node), boundedText(entry.Storage)
		key := node + "/" + name
		if entry.Type != "storage" || name == "" || seen[key] {
			continue
		}
		seen[key] = true
		storages = append(storages, Storage{
			Node: node, Name: name, Type: boundedText(entry.PluginType),
			Total: entry.MaxDisk, Used: entry.Disk, Avail: max(entry.MaxDisk-entry.Disk, 0),
			Content: splitContent(entry.Content), Active: entry.Status == "available",
		})
	}
	return storages, nil
}

// BackupEntry est une archive de sauvegarde d'un stockage.
type BackupEntry struct {
	VMID      int
	CreatedAt time.Time
	Size      int64
}

// Backups liste les archives de sauvegarde d'un stockage d'un nœud. Un nœud et
// un stockage viennent de l'API : ils sont échappés avant d'entrer dans le chemin.
func (c *Client) Backups(ctx context.Context, node, storage string) ([]BackupEntry, error) {
	var listed []struct {
		VMID  int   `json:"vmid"`
		CTime int64 `json:"ctime"`
		Size  int64 `json:"size"`
	}
	path := "/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	if err := c.get(ctx, path, url.Values{"content": {ContentBackup}}, &listed); err != nil {
		return nil, err
	}
	entries := make([]BackupEntry, 0, len(listed))
	for _, entry := range listed {
		// Une archive sans vmid n'est pas celle d'une VM connue.
		if entry.VMID <= 0 || entry.CTime <= 0 {
			continue
		}
		entries = append(entries, BackupEntry{VMID: entry.VMID, CreatedAt: time.Unix(entry.CTime, 0).UTC(), Size: entry.Size})
	}
	return entries, nil
}

func splitContent(content string) []string {
	contents := []string{}
	for _, item := range strings.FieldsFunc(content, func(r rune) bool { return r == ',' }) {
		if len(contents) == maxContents {
			break
		}
		contents = append(contents, boundedText(item))
	}
	return contents
}

// boundedText coupe un texte de l'API à maxTextBytes, sans couper un caractère
// en deux.
func boundedText(text string) string {
	if len(text) <= maxTextBytes {
		return text
	}
	cut := maxTextBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
