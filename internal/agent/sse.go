package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// ErrEventTooLarge : le serveur a envoyé une ligne ou un événement plus long
// que ce que le protocole permet. Le flux est coupé : ce n'est pas openCloud
// qui parle, ou il s'est trompé.
var ErrEventTooLarge = errors.New("stream event too large")

// readEvent lit un événement SSE : des lignes « event: » et « data: »
// jusqu'à une ligne vide. Les commentaires (« : ping ») sont ignorés. Les
// lignes « data: » se recollent sans séparateur (agentwire.EventDataLine).
// Une ligne se borne à la taille du tampon du lecteur
// (agentwire.MaxEventLineBytes), un événement à agentwire.MaxEventBytes. Le
// lecteur est partagé entre les appels : ce qu'il a lu d'avance reste à lui.
func readEvent(reader *bufio.Reader) (name, data string, err error) {
	var content strings.Builder
	hasData := false
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", "", fmt.Errorf("%w: a line beyond %d bytes", ErrEventTooLarge, reader.Size())
		}
		if err != nil {
			if err == io.EOF && (name != "" || hasData) {
				return name, content.String(), nil
			}
			return "", "", err
		}
		text := strings.TrimRight(string(line), "\r\n")
		if text == "" {
			if name != "" || hasData {
				return name, content.String(), nil
			}
			continue
		}
		field, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			hasData = true
			if content.Len()+len(value) > agentwire.MaxEventBytes {
				return "", "", fmt.Errorf("%w: an event beyond %d bytes", ErrEventTooLarge, agentwire.MaxEventBytes)
			}
			content.WriteString(value)
		}
	}
}
