package dockerapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"
)

// Une ligne d'événement tient bien en dessous : les attributs sont les
// labels du conteneur, ceux de l'image compris, et son auteur les écrit.
const maxEventLine = 256 << 10

// ErrEventLineTooLong dit qu'une ligne du flux dépassait maxEventLine et a
// été jetée : le flux, lui, continue. Elle n'est pas fatale, sans quoi la
// reconnexion rejouerait la même ligne en tête du flux, à l'infini.
var ErrEventLineTooLong = errors.New("docker event line too long, skipped")

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

// signalKill est le numéro de SIGKILL tel qu'un « kill » le porte.
const signalKill = "9"

// IsStopSignal dit si un « kill » envoie autre chose que SIGKILL : c'est
// ainsi que docker stop commence, même avec un délai nul, avec le signal
// d'arrêt du conteneur. docker kill envoie SIGKILL d'emblée.
func (e Event) IsStopSignal() bool {
	signal := e.Actor.Attributes["signal"]
	return e.Action == "kill" && signal != "" && signal != signalKill
}

// EventStream est le flux ouvert ; Next bloque jusqu'au prochain événement
// ou à la fin du flux, Close raccroche.
type EventStream struct {
	body   io.ReadCloser
	reader *bufio.Reader
	// line est la ligne en cours, réutilisée d'un appel à l'autre ; elle ne
	// dépasse jamais maxEventLine.
	line []byte
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
	return &EventStream{body: response.Body, reader: bufio.NewReaderSize(response.Body, 64<<10)}, nil
}

// Next rend l'événement suivant ; io.EOF à la fin du flux.
// ErrEventLineTooLong dit qu'une ligne a été sautée : l'appelant le note et
// rappelle Next, le flux n'est pas perdu.
func (s *EventStream) Next() (Event, error) {
	for {
		line, err := s.readLine()
		if err != nil {
			return Event{}, err
		}
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return Event{}, fmt.Errorf("decode event: %w", err)
		}
		return event, nil
	}
}

// readLine lit jusqu'au saut de ligne sans jamais garder plus de
// maxEventLine octets : au-delà, la fin de la ligne est lue et jetée, puis
// ErrEventLineTooLong est rendue. Un bufio.Scanner s'arrêterait net, et le
// démon rejouerait la ligne fautive à chaque reconnexion.
func (s *EventStream) readLine() ([]byte, error) {
	s.line = s.line[:0]
	tooLong := false
	for {
		chunk, err := s.reader.ReadSlice('\n')
		if err == nil {
			chunk = chunk[:len(chunk)-1]
		}
		if !tooLong {
			if len(s.line)+len(chunk) > maxEventLine {
				tooLong = true
				s.line = s.line[:0]
			} else {
				s.line = append(s.line, chunk...)
			}
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err == nil:
			if tooLong {
				return nil, ErrEventLineTooLong
			}
			return s.line, nil
		case tooLong:
			// La fin du flux, ou son erreur, se redira à l'appel suivant.
			return nil, ErrEventLineTooLong
		case errors.Is(err, io.EOF) && len(s.line) > 0:
			return s.line, nil
		}
		return nil, err
	}
}

func (s *EventStream) Close() error {
	return s.body.Close()
}
