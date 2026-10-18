package alert

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// matrixMessage est un m.room.message : le texte brut pour les clients
// qui n'affichent rien d'autre, et un HTML simple pour les autres.
type matrixMessage struct {
	MsgType       string `json:"msgtype"`
	Body          string `json:"body"`
	Format        string `json:"format"`
	FormattedBody string `json:"formatted_body"`
}

func matrixBody(title string, text Text, link, openLabel string) (Body, error) {
	plain := plainText(title, text)
	formatted := "<strong>" + html.EscapeString(title) + "</strong><br>" + html.EscapeString(text.Cause)
	if text.Action != "" {
		formatted += "<br>" + html.EscapeString(text.Action)
	}
	if link != "" {
		plain += "\n" + openLabel + " : " + link
		formatted += `<br><a href="` + html.EscapeString(link) + `">` + html.EscapeString(openLabel) + "</a>"
	}
	return jsonBody(title, link, matrixMessage{
		MsgType: "m.text", Body: plain, Format: "org.matrix.custom.html", FormattedBody: formatted,
	})
}

// matrixTransactionID nomme un envoi auprès du serveur d'accueil : le même
// identifiant rejoué avec le même jeton n'écrit qu'un message. Une
// livraison garde le sien d'un essai à l'autre et après un redémarrage ;
// un test en tire un neuf.
func matrixTransactionID(delivery Delivery) (string, error) {
	if delivery.ID != 0 {
		return brand.MatrixTransactionPrefix + strconv.FormatInt(delivery.ID, 10) + "-" + strconv.FormatInt(delivery.CreatedAt.Unix(), 10), nil
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return brand.MatrixTransactionPrefix + "test-" + hex.EncodeToString(random), nil
}

// matrixSendURL est l'adresse de l'envoi dans le salon, sous le serveur
// d'accueil ; l'identifiant du salon et de la transaction sont échappés.
func matrixSendURL(homeserver, roomID, transactionID string) string {
	return strings.TrimRight(homeserver, "/") + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/send/m.room.message/" + url.PathEscape(transactionID)
}

// sendMatrix écrit le message dans le salon, au nom du bot dont le canal
// tient le jeton. Le jeton part en en-tête, jamais dans l'URL ni le corps.
func (n *Notifier) sendMatrix(ctx context.Context, channel Channel, body Body) Outcome {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	target := matrixSendURL(channel.URL, channel.Destination, body.TransactionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(body.Content))
	if err != nil {
		return Outcome{Reason: ReasonUnreachable}
	}
	request.Header.Set("Content-Type", body.ContentType)
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Authorization", "Bearer "+channel.Secret)
	response, err := n.client.Do(request)
	if err != nil {
		return Outcome{Reason: reasonFor(err)}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBytes))
	return outcomeOf(response.StatusCode)
}
