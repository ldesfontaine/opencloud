package httpx

import (
	"fmt"
	"net/http"
)

// WriteEvent écrit un événement SSE et le pousse tout de suite : le flux ne
// bufferise pas. data tient sur une ligne et vient du serveur (un mot, un
// jeton, du JSON encodé), jamais d'une entrée.
func WriteEvent(w http.ResponseWriter, name, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil { // #nosec G705 -- text/event-stream, données du serveur, jamais une entrée.
		return err
	}
	return http.NewResponseController(w).Flush()
}
