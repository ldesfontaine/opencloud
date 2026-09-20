package dockerapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"
)

// Une ligne d'événement tient bien en dessous : les attributs sont les
// labels du conteneur.
const maxEventLine = 256 << 10

// Event est un événement de conteneur ou de réseau tel que le démon le
// pousse. Sur un réseau, l'acteur est le réseau et l'attribut « container »
// dit qui s'y connecte.
type Event struct {
	Type   string
	Action string
	Actor  struct {
		ID         string
		Attributes map[string]string
	}
	Time     int64 `json:"time"`
	TimeNano int64 `json:"timeNano"`
}

// At rend l'instant de l'événement, à la nanoseconde quand le démon la
// donne : elle sert à repartir du bon endroit à la reconnexion.
func (e Event) At() time.Time {
	if e.TimeNano > 0 {
		return time.Unix(0, e.TimeNano).UTC()
	}
	return time.Unix(e.Time, 0).UTC()
}

// Since rend ce que le paramètre « since » attend pour ne rien manquer
// après cet événement : secondes et nanosecondes, sans arrondi.
func (e Event) Since() string {
	if e.TimeNano > 0 {
		return strconv.FormatInt(e.TimeNano/1e9, 10) + "." + fmt.Sprintf("%09d", e.TimeNano%1e9)
	}
	return strconv.FormatInt(e.Time, 10)
}

// ExitCode lit le code de sortie que porte un « die » ; -1 sans lui.
func (e Event) ExitCode() int {
	code, err := strconv.Atoi(e.Actor.Attributes["exitCode"])
	if err != nil {
		return -1
	}
	return code
}

// EventStream est le flux ouvert ; Next bloque jusqu'au prochain événement
// ou à la fin du flux, Close raccroche.
type EventStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

// Events ouvre le flux des événements de conteneurs et de réseaux ; since,
// s'il n'est pas vide, rejoue d'abord ceux survenus depuis cet instant. Le
// contexte tient le flux : l'annuler le ferme.
func (c *Client) Events(ctx context.Context, since string) (*EventStream, error) {
	query := url.Values{"filters": {`{"type":["container","network"]}`}}
	if since != "" {
		query.Set("since", since)
	}
	response, err := c.get(ctx, "/events?"+query.Encode())
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)
	return &EventStream{body: response.Body, scanner: scanner}, nil
}

func (s *EventStream) Next() (Event, error) {
	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return Event{}, fmt.Errorf("decode event: %w", err)
		}
		return event, nil
	}
	if err := s.scanner.Err(); err != nil {
		return Event{}, err
	}
	return Event{}, io.EOF
}

func (s *EventStream) Close() error {
	return s.body.Close()
}
