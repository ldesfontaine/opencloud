package dockerapi

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

const dieLine = `{"Action":"die","Actor":{"Attributes":{"exitCode":"1"},"ID":"0082fc783011ead4e11be27f298f845fc39b25b371c0247ed7b7a798b8388513"},"Type":"container","time":1789802649,"timeNano":1789802649736266200}` + "\n"

func eventsBody(t *testing.T, body string) *Client {
	return fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
}

// Une ligne d'événement trop longue (les labels d'une image) est sautée, dite
// par ErrEventLineTooLong, et les suivantes passent : sans cela le démon la
// rejouerait en tête de chaque reconnexion et plus rien ne serait lu.
func TestEvents_SkipsALineOverTheLimitAndGoesOn(t *testing.T) {
	huge := `{"Action":"start","Actor":{"Attributes":{"label":"` + strings.Repeat("x", 300<<10) + `"},"ID":"a"},"Type":"container","timeNano":1789802648000000000}` + "\n"
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(huge + dieLine + strings.TrimSuffix(huge, "\n")))
	}))
	stream, err := client.Events(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, ErrEventLineTooLong) {
		t.Fatalf("first = %v, want the line skipped", err)
	}
	event, err := stream.Next()
	if err != nil || event.Action != "die" || event.ExitCode() != 1 {
		t.Fatalf("second = %+v, %v", event, err)
	}
	// Une ligne trop longue en fin de flux, sans saut de ligne : sautée aussi,
	// puis la fin du flux.
	if _, err := stream.Next(); !errors.Is(err, ErrEventLineTooLong) {
		t.Fatalf("third = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("fourth = %v, want the end of the stream", err)
	}
}

func TestEvents_ALineExactlyAtTheLimitIsRead(t *testing.T) {
	prefix := `{"Action":"start","Actor":{"Attributes":{"label":"`
	suffix := `"},"ID":"a"},"Type":"container","timeNano":1789802648000000000}`
	line := prefix + strings.Repeat("x", maxEventLine-len(prefix)-len(suffix)) + suffix
	if len(line) != maxEventLine {
		t.Fatalf("line is %d bytes", len(line))
	}
	stream, err := eventsBody(t, line+"\n"+line+"0\n"+dieLine).Events(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, err := stream.Next(); err != nil || event.Action != "start" {
		t.Fatalf("at the limit = %+v, %v", event, err)
	}
	if _, err := stream.Next(); !errors.Is(err, ErrEventLineTooLong) {
		t.Fatalf("one byte over = %v", err)
	}
	if event, err := stream.Next(); err != nil || event.Action != "die" {
		t.Fatalf("after = %+v, %v", event, err)
	}
}

// Sans saut de ligne, 8 Mio de sortie d'un conteneur TTY ne font jamais une
// ligne de plus de maxLogLine : la lecture ne garde pas plus que la borne.
func TestLogs_TTYLineIsCutAtTheLimit(t *testing.T) {
	const total = 8 << 20
	stamp := "2026-09-19T07:24:02.000000000Z "
	body := stamp + strings.Repeat("a", total) + "\nsuivante\n"
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	reader, err := client.Logs(context.Background(), "x", LogsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var lines, bytesRead int
	var last Line
	for {
		line, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// L'horodatage est retiré de la première ligne seulement.
		if size := len(line.Text); size > maxLogLine {
			t.Fatalf("line %d has %d bytes, over %d", lines, size, maxLogLine)
		}
		lines++
		bytesRead += len(line.Text)
		last = line
	}
	if last.Text != "suivante" {
		t.Fatalf("last line = %q", last.Text)
	}
	if want := total + len("suivante"); bytesRead != want {
		t.Fatalf("read %d bytes, want %d: nothing is lost by the cut", bytesRead, want)
	}
	if lines < total/maxLogLine {
		t.Fatalf("lines = %d", lines)
	}
}

// Un cadre multiplexé de 8 Mio, dont l'en-tête dit la taille, est lu par
// morceaux : des lignes de maxLogLine au plus, sans octet perdu, et le cadre
// suivant reste lisible.
func TestLogs_AHugeFrameIsReadInPieces(t *testing.T) {
	const total = 8 << 20
	frame := func(stream byte, payload string) string {
		header := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
		return string(header) + payload
	}
	body := frame(frameStdout, "2026-09-19T07:24:02.000000000Z "+strings.Repeat("a", total)+"\n") +
		frame(frameStderr, "2026-09-19T07:24:03.000000000Z suivante\n")
	client := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	reader, err := client.Logs(context.Background(), "x", LogsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var bytesRead int
	var last Line
	for {
		line, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if size := len(line.Text); size > maxLogLine {
			t.Fatalf("a line has %d bytes, over %d", size, maxLogLine)
		}
		bytesRead += len(line.Text)
		last = line
	}
	if last.Text != "suivante" || last.Stream != "stderr" {
		t.Fatalf("last line = %+v", last)
	}
	if want := total + len("suivante"); bytesRead != want {
		t.Fatalf("read %d bytes, want %d", bytesRead, want)
	}
}
