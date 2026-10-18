package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/egress"
)

const (
	// DefaultBaseURL : la seule adresse que ce composant joint.
	DefaultBaseURL = "https://endoflife.date/api/v1"
	requestTimeout = 20 * time.Second
	// Des réponses bornées : la liste des identifiants pèse quelques
	// centaines de Kio, un produit quelques dizaines.
	maxIdentifiersBytes = 4 << 20
	maxProductBytes     = 1 << 20
	dockerIdentifier    = "pkg:docker/"
	// Ce qu'on garde d'un produit, pour qu'une réponse démesurée ne
	// remplisse ni la base ni l'écran.
	maxCycles      = 200
	maxLabelLength = 80
	userAgent      = brand.LifecycleUserAgent
)

var (
	// ErrSource : la source n'a pas répondu, ou pas ce qu'on attend.
	ErrSource = errors.New("lifecycle source failed")
	// ErrNoProduct : la source a répondu qu'elle n'a pas ce qu'on demande.
	// C'est aussi un ErrSource ; pour un produit, ce n'est pas une panne.
	ErrNoProduct = errors.New("lifecycle product not found")
	// Un nom de produit ou de dépôt tel que la source les écrit.
	productPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,63}$`)
	repositoryPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,200}$`)
	dayPattern        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// Un nom de branche ou de version : « 16 », « 3.1 », « 24.04 »,
	// « 2.9.2.1 ». Ce qui n'y ressemble pas n'est pas gardé : ces textes
	// viennent d'un tiers et finissent à l'écran, et chez un agent IA.
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)
)

// Source est ce que le composant attend d'endoflife.date.
type Source interface {
	// Repositories rend, pour chaque dépôt Docker que la source connaît,
	// le nom de son produit.
	Repositories(ctx context.Context) (map[string]string, error)
	// Product rend ErrNoProduct quand la source ne connaît pas ce nom.
	Product(ctx context.Context, name string) (Product, error)
}

// HTTPSource lit l'API publique, sans compte ni clé.
type HTTPSource struct {
	base   string
	client *http.Client
}

// NewHTTPSource compose par egress.Dial, comme tout ce qui sort
// d'openCloud : l'adresse de métadonnées d'un hébergeur reste hors
// d'atteinte, redirection comprise.
func NewHTTPSource() *HTTPSource {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return egress.Dial(ctx, network, address, requestTimeout)
		},
		TLSHandshakeTimeout:   requestTimeout,
		ResponseHeaderTimeout: requestTimeout,
	}
	// Une API JSON ne redirige pas ; une redirection vers une adresse privée
	// ne doit pas être composée, comme pour le registre et les canaux.
	client := &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return NewHTTPSourceAt(DefaultBaseURL, client)
}

// NewHTTPSourceAt vise une autre adresse : les tests y posent un serveur.
func NewHTTPSourceAt(base string, client *http.Client) *HTTPSource {
	return &HTTPSource{base: strings.TrimSuffix(base, "/"), client: client}
}

type identifiersBody struct {
	Result []struct {
		Identifier string `json:"identifier"`
		Product    struct {
			Name string `json:"name"`
		} `json:"product"`
	} `json:"result"`
}

func (s *HTTPSource) Repositories(ctx context.Context) (map[string]string, error) {
	var body identifiersBody
	if err := s.get(ctx, "/identifiers/purl/", maxIdentifiersBytes, &body); err != nil {
		return nil, err
	}
	repositories := make(map[string]string)
	for _, entry := range body.Result {
		repository, isDocker := dockerRepository(entry.Identifier)
		if !isDocker || !productPattern.MatchString(entry.Product.Name) {
			continue
		}
		repositories[repository] = entry.Product.Name
	}
	return repositories, nil
}

// dockerRepository lit « pkg:docker/library/postgres » : le dépôt, sans
// version ni qualificatifs.
func dockerRepository(identifier string) (string, bool) {
	repository, isDocker := strings.CutPrefix(identifier, dockerIdentifier)
	if !isDocker {
		return "", false
	}
	if end := strings.IndexAny(repository, "@?#"); end >= 0 {
		repository = repository[:end]
	}
	return repository, repositoryPattern.MatchString(repository)
}

// day ne garde qu'un jour bien formé : la source écrit une date ou null,
// et tout le reste se lit comme « pas de date ».
type day string

func (d *day) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !dayPattern.MatchString(text) {
		*d = ""
		return nil
	}
	*d = day(text)
	return nil
}

type productBody struct {
	Result struct {
		Name     string `json:"name"`
		Label    string `json:"label"`
		Releases []struct {
			Name         string `json:"name"`
			IsEOL        bool   `json:"isEol"`
			EOLFrom      day    `json:"eolFrom"`
			IsMaintained bool   `json:"isMaintained"`
			Latest       *struct {
				Name string `json:"name"`
			} `json:"latest"`
		} `json:"releases"`
	} `json:"result"`
}

func (s *HTTPSource) Product(ctx context.Context, name string) (Product, error) {
	if !productPattern.MatchString(name) {
		return Product{}, fmt.Errorf("%w: bad product name", ErrSource)
	}
	var body productBody
	if err := s.get(ctx, "/products/"+url.PathEscape(name)+"/", maxProductBytes, &body); err != nil {
		return Product{}, err
	}
	product := Product{Name: name, Label: labelOf(body.Result.Label)}
	if product.Label == "" {
		product.Label = name
	}
	for _, release := range body.Result.Releases {
		if !versionPattern.MatchString(release.Name) || len(product.Cycles) == maxCycles {
			continue
		}
		cycle := Cycle{Name: release.Name, IsEOL: release.IsEOL, EOLFrom: string(release.EOLFrom), IsMaintained: release.IsMaintained}
		if release.Latest != nil && versionPattern.MatchString(release.Latest.Name) {
			cycle.Latest = release.Latest.Name
		}
		product.Cycles = append(product.Cycles, cycle)
	}
	return product, nil
}

func (s *HTTPSource) get(ctx context.Context, path string, limit int64, into any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSource, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSource, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %w: %s", ErrSource, ErrNoProduct, path)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d on %s", ErrSource, response.StatusCode, path)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSource, err)
	}
	if int64(len(content)) > limit {
		return fmt.Errorf("%w: response over %d bytes on %s", ErrSource, limit, path)
	}
	if err := json.Unmarshal(content, into); err != nil {
		return fmt.Errorf("%w: %w", ErrSource, err)
	}
	return nil
}

// labelOf garde du nom affiché d'un produit ses caractères imprimables,
// sur une ligne, coupé à sa longueur utile.
func labelOf(text string) string {
	printable := strings.Map(func(character rune) rune {
		if unicode.IsPrint(character) {
			return character
		}
		return -1
	}, text)
	runes := []rune(strings.TrimSpace(printable))
	if len(runes) > maxLabelLength {
		runes = runes[:maxLabelLength]
	}
	return string(runes)
}
