package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Record : un enregistrement DNS, réduit à ce qu'openCloud écrit et relit.
// Cloudflare rend beaucoup d'autres champs (created_on, proxiable, meta…) :
// ils ne servent à rien ici et ne sont pas décodés.
//
// Forme du champ result, d'après la documentation
// (https://developers.cloudflare.com/api/ → DNS › Records) :
//
//	{"id": "372e67954025e0ba6aaa6d586b9e0b59", "zone_id": "023e105f…",
//	 "name": "_acme-challenge.example.com", "type": "TXT",
//	 "content": "…", "ttl": 120, "proxied": false}
type Record struct {
	ID      string `json:"id,omitempty"`
	ZoneID  string `json:"zone_id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	// Sans omitempty : « proxied: false » se dit, il ne se devine pas.
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

const recordsPath = "/zones/"

// Records liste les enregistrements d'une zone. Un nom et un type vides ne
// filtrent rien.
func (c *Client) Records(ctx context.Context, token, zoneID, name, recordType string) ([]Record, error) {
	query := url.Values{}
	if name != "" {
		query.Set("name", name)
	}
	if recordType != "" {
		query.Set("type", recordType)
	}

	result, err := c.call(ctx, token, http.MethodGet, recordsPath+zoneID+"/dns_records", query, nil)
	if err != nil {
		return nil, err
	}

	var records []Record
	if err := json.Unmarshal(result, &records); err != nil {
		return nil, ErrUnreadableReply
	}
	return records, nil
}

// CreateRecord écrit un enregistrement et rend celui que Cloudflare a créé,
// avec son identifiant.
func (c *Client) CreateRecord(ctx context.Context, token, zoneID string, record Record) (Record, error) {
	// L'identifiant et la zone ne s'envoient pas : c'est l'URL qui dit la zone,
	// et Cloudflare qui donne l'identifiant.
	record.ID = ""
	record.ZoneID = ""

	result, err := c.call(ctx, token, http.MethodPost, recordsPath+zoneID+"/dns_records", nil, record)
	if err != nil {
		return Record{}, err
	}

	var created Record
	if err := json.Unmarshal(result, &created); err != nil {
		return Record{}, ErrUnreadableReply
	}
	return created, nil
}

// DeleteRecord retire un enregistrement. Cloudflare rend {"result": {"id": …}}.
func (c *Client) DeleteRecord(ctx context.Context, token, zoneID, recordID string) error {
	_, err := c.call(ctx, token, http.MethodDelete, recordsPath+zoneID+"/dns_records/"+recordID, nil, nil)
	return err
}
