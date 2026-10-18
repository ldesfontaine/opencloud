package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// Message est ce qu'on envoie : un sujet, un texte brut, et un HTML
// simple facultatif qui part en variante.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

const (
	senderName = brand.ProductName
	crlf       = "\r\n"
)

// Compose écrit le message au format RFC 5322 : en-têtes propres, sujet
// encodé en RFC 2047 quand il le faut, corps en quoted-printable UTF-8.
func Compose(from string, message Message, now time.Time) ([]byte, error) {
	messageID, err := newMessageID(from)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	writeHeader(&out, "From", mime.QEncoding.Encode("utf-8", senderName)+" <"+from+">")
	writeHeader(&out, "To", strings.Join(message.To, ", "))
	writeHeader(&out, "Subject", mime.QEncoding.Encode("utf-8", oneLine(message.Subject)))
	writeHeader(&out, "Date", now.Format(time.RFC1123Z))
	writeHeader(&out, "Message-ID", messageID)
	// Une réponse automatique ne doit pas répondre à une alerte (RFC 3834).
	writeHeader(&out, "Auto-Submitted", "auto-generated")
	writeHeader(&out, "MIME-Version", "1.0")
	if message.HTML == "" {
		if err := writePart(&out, "text/plain", message.Text); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	boundary, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	writeHeader(&out, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	out.WriteString(crlf)
	for _, part := range []struct{ contentType, content string }{{"text/plain", message.Text}, {"text/html", message.HTML}} {
		out.WriteString("--" + boundary + crlf)
		if err := writePart(&out, part.contentType, part.content); err != nil {
			return nil, err
		}
	}
	out.WriteString("--" + boundary + "--" + crlf)
	return out.Bytes(), nil
}

func writeHeader(out *bytes.Buffer, name, value string) {
	out.WriteString(name + ": " + value + crlf)
}

// writePart écrit l'en-tête d'une partie, une ligne vide, puis le corps
// en quoted-printable, qui garde les accents et coupe les longues lignes.
func writePart(out *bytes.Buffer, contentType, content string) error {
	writeHeader(out, "Content-Type", contentType+"; charset=utf-8")
	writeHeader(out, "Content-Transfer-Encoding", "quoted-printable")
	out.WriteString(crlf)
	writer := quotedprintable.NewWriter(out)
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\n", crlf)
	if _, err := writer.Write([]byte(normalized)); err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	out.WriteString(crlf)
	return nil
}

// oneLine retire les retours à la ligne : un sujet ne s'étend jamais sur
// un en-tête de plus.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// newMessageID tire un identifiant sur le domaine de l'expéditeur : c'est
// ce que les filtres attendent d'un message légitime.
func newMessageID(from string) (string, error) {
	random, err := randomHex(16)
	if err != nil {
		return "", err
	}
	domain := from[strings.LastIndexByte(from, '@')+1:]
	return "<" + random + "@" + domain + ">", nil
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("draw random: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
