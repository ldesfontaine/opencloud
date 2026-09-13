package agent

import (
	"bufio"
	"io"
	"strings"
)

// readEvent lit un événement SSE : des lignes « event: » et « data: »
// jusqu'à une ligne vide. Les commentaires (« : ping ») sont ignorés. Le
// lecteur est partagé entre les appels : ce qu'il a lu d'avance reste à lui.
func readEvent(reader *bufio.Reader) (name, data string, err error) {
	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && (name != "" || len(lines) > 0) {
				return name, strings.Join(lines, "\n"), nil
			}
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if name != "" || len(lines) > 0 {
				return name, strings.Join(lines, "\n"), nil
			}
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			name = value
		case "data":
			lines = append(lines, value)
		}
	}
}
