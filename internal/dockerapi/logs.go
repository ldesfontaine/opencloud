package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// Un cadre du multiplexage : type sur un octet, trois réservés, taille
	// sur quatre.
	frameHeaderSize = 8
	frameStdout     = 1
	frameStderr     = 2
	// Une ligne de journal plus longue est coupée, pas refusée.
	maxLogLine = 64 << 10
)

// Line est une ligne de journal : son instant, son flux, son texte.
type Line struct {
	At     time.Time
	Stream string
	Text   string
}

type LogsOptions struct {
	Tail   int
	Follow bool
}

// LogReader rend les lignes d'un journal, démultiplexées si le conteneur
// n'a pas de TTY ; sinon le flux est brut, c'est le premier cadre qui le
// dit. Une ligne peut être à cheval sur deux cadres : les octets attendent
// dans pending jusqu'au retour à la ligne.
type LogReader struct {
	body   io.ReadCloser
	reader *bufio.Reader
	// tty est décidé au premier cadre lu ; avant, on ne sait pas.
	decided bool
	tty     bool
	pending []byte
	stream  string
	// Un en-tête lu d'avance, quand le flux change au milieu d'une ligne.
	next *frameHeader
}

type frameHeader struct {
	stream string
	size   int
}

// Logs ouvre le journal d'un conteneur, horodaté par le démon ; Follow
// garde le flux ouvert jusqu'à l'arrêt du conteneur ou du contexte.
func (c *Client) Logs(ctx context.Context, id string, opts LogsOptions) (*LogReader, error) {
	query := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}}
	if opts.Tail > 0 {
		query.Set("tail", strconv.Itoa(opts.Tail))
	}
	if opts.Follow {
		query.Set("follow", "1")
	}
	response, err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/logs?"+query.Encode())
	if err != nil {
		return nil, err
	}
	return &LogReader{body: response.Body, reader: bufio.NewReaderSize(response.Body, 32<<10), stream: "stdout"}, nil
}

// Next rend la ligne suivante ; io.EOF à la fin du journal.
func (r *LogReader) Next() (Line, error) {
	if !r.decided {
		if err := r.decide(); err != nil {
			return Line{}, err
		}
	}
	if r.tty {
		raw, err := r.reader.ReadBytes('\n')
		if len(raw) == 0 && err != nil {
			return Line{}, err
		}
		return parseLine(raw, r.stream), nil
	}
	return r.nextFramed()
}

// decide regarde le premier cadre : un type 1 ou 2 suivi de trois zéros ne
// commence aucun texte, c'est un journal multiplexé.
func (r *LogReader) decide() error {
	head, err := r.reader.Peek(frameHeaderSize)
	if err != nil && len(head) == 0 {
		return err
	}
	r.decided = true
	isFrame := len(head) == frameHeaderSize && (head[0] == frameStdout || head[0] == frameStderr) && head[1] == 0 && head[2] == 0 && head[3] == 0
	r.tty = !isFrame
	return nil
}

// nextFramed rend une ligne complète de pending, sinon lit un cadre de
// plus ; un changement de flux ou la fin du journal clôt la ligne en cours.
func (r *LogReader) nextFramed() (Line, error) {
	for {
		if index := bytes.IndexByte(r.pending, '\n'); index >= 0 || len(r.pending) >= maxLogLine {
			if index < 0 {
				index = len(r.pending) - 1
			}
			line := parseLine(r.pending[:index+1], r.stream)
			r.pending = append([]byte(nil), r.pending[index+1:]...)
			return line, nil
		}
		header, err := r.readHeader()
		if err != nil {
			return r.flush(err)
		}
		if header.stream != r.stream && len(r.pending) > 0 {
			r.next = &header
			return r.flush(nil)
		}
		r.stream = header.stream
		frame := make([]byte, header.size)
		if _, err := io.ReadFull(r.reader, frame); err != nil {
			return r.flush(err)
		}
		r.pending = append(r.pending, frame...)
	}
}

// flush rend ce qui reste comme dernière ligne, sinon l'erreur.
func (r *LogReader) flush(err error) (Line, error) {
	if len(r.pending) == 0 {
		if err == nil {
			err = io.EOF
		}
		return Line{}, err
	}
	line := parseLine(r.pending, r.stream)
	r.pending = nil
	if r.next != nil {
		r.stream = r.next.stream
	}
	return line, nil
}

func (r *LogReader) readHeader() (frameHeader, error) {
	if r.next != nil {
		header := *r.next
		r.next = nil
		return header, nil
	}
	var raw [frameHeaderSize]byte
	if _, err := io.ReadFull(r.reader, raw[:]); err != nil {
		return frameHeader{}, err
	}
	header := frameHeader{stream: "stdout", size: int(binary.BigEndian.Uint32(raw[4:]))}
	if raw[0] == frameStderr {
		header.stream = "stderr"
	}
	if header.size == 0 {
		return frameHeader{}, fmt.Errorf("empty log frame")
	}
	return header, nil
}

func (r *LogReader) Close() error {
	return r.body.Close()
}

// parseLine sépare l'horodatage que le démon met devant chaque ligne.
func parseLine(raw []byte, stream string) Line {
	text := strings.TrimRight(string(raw), "\r\n")
	stamp, rest, ok := strings.Cut(text, " ")
	if !ok {
		return Line{Stream: stream, Text: text}
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return Line{Stream: stream, Text: text}
	}
	return Line{At: at.UTC(), Stream: stream, Text: rest}
}
