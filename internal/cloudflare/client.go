package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL : la racine de l'API v4. Injectable pour les tests et pour le
// test de bout en bout, qui lance un faux Cloudflare.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Un appel à Cloudflare est court : il interroge une API, il n'attend pas la
// propagation d'une zone. Passé ce délai, mieux vaut le dire que patienter.
const requestTimeout = 15 * time.Second

// Une réponse d'API tient en quelques kibioctets ; au-delà, quelque chose
// d'autre répond à notre place et on ne le lira pas en entier.
const maxResponseBytes = 1 << 20

// Les statuts d'un jeton, tels que /user/tokens/verify les rend.
const tokenActive = "active"

// Client parle à l'API v4. Il ne garde aucun jeton : chaque appel reçoit celui
// qu'il doit présenter.
type Client struct {
	baseURL string
	http    *http.Client
}

// New monte le client sur une racine d'API. Une racine vide prend celle de
// Cloudflare.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// envelope : l'enveloppe commune à toute réponse de l'API v4. Forme relevée
// dans la documentation, https://developers.cloudflare.com/api/ :
//
//	{"success": true, "errors": [], "messages": [], "result": …}
type envelope struct {
	Success  bool            `json:"success"`
	Errors   []apiMessage    `json:"errors"`
	Messages []apiMessage    `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

// apiMessage : un élément de errors[] ou de messages[] — un code et une phrase
// en anglais, telle que Cloudflare l'écrit.
type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call joue une requête et rend le champ result. body nil pour un GET ou un
// DELETE. Le jeton part dans l'en-tête, jamais dans l'URL ni dans le corps.
func (c *Client) call(ctx context.Context, token, method, path string, query url.Values, body any) (json.RawMessage, error) {
	request, err := c.newRequest(ctx, token, method, path, query, body)
	if err != nil {
		return nil, err
	}

	response, err := c.http.Do(request)
	if err != nil {
		// L'URL peut apparaître ici, jamais le jeton : il n'est que dans un
		// en-tête, et net/http ne le recopie pas dans ses erreurs.
		return nil, fmt.Errorf("call cloudflare %s: %w", path, err)
	}
	defer response.Body.Close()

	read, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read cloudflare reply for %s: %w", path, err)
	}

	var decoded envelope
	if err := json.Unmarshal(read, &decoded); err != nil {
		return nil, fmt.Errorf("read cloudflare reply for %s: %w", path, ErrUnreadableReply)
	}
	if !decoded.Success {
		return nil, newAPIError(response.StatusCode, decoded.Errors)
	}
	return decoded.Result, nil
}

func (c *Client) newRequest(ctx context.Context, token, method, path string, query url.Values, body any) (*http.Request, error) {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode cloudflare request for %s: %w", path, err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, fmt.Errorf("build cloudflare request for %s: %w", path, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

// VerifyToken demande à Cloudflare si le jeton vaut encore quelque chose.
// GET /user/tokens/verify rend {"result": {"id": …, "status": "active"}}.
func (c *Client) VerifyToken(ctx context.Context, token string) error {
	result, err := c.call(ctx, token, http.MethodGet, "/user/tokens/verify", nil, nil)
	if err != nil {
		// Un jeton refusé se dit d'un mot : c'est un refus pour l'opérateur,
		// pas une panne.
		var apiError *APIError
		if errors.As(err, &apiError) && apiError.Unauthorized() {
			return ErrInvalidToken
		}
		return err
	}

	var verified struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &verified); err != nil {
		return ErrUnreadableReply
	}
	if verified.Status != tokenActive {
		return fmt.Errorf("%w: status %q", ErrInvalidToken, verified.Status)
	}
	return nil
}
