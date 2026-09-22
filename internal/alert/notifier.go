package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/egress"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

const (
	userAgent     = "openCloud"
	queueSize     = 1024
	workerCount   = 4
	maxErrorBytes = 64 << 10
)

// Ce que le notifieur attend de la base : noter l'issue d'une livraison.
type DeliveryStore interface {
	UpdateDelivery(ctx context.Context, delivery Delivery) error
}

// Outcome est ce qu'un envoi a donné : rien à dire, ou un motif et le
// code HTTP reçu s'il y en a eu un.
type Outcome struct {
	Reason Reason
	Code   int
}

func (o Outcome) OK() bool {
	return o.Reason == ReasonNone
}

// Notifier livre les webhooks : une file, quelques ouvriers, trois essais
// par livraison. Le client compose par la garde de sortie et ne suit
// aucune redirection : un 3xx est un échec, il n'y a rien à suivre chez
// un récepteur de webhooks.
type Notifier struct {
	store    DeliveryStore
	catalogs lang.Catalogs
	language func() lang.Code
	client   *http.Client
	logger   *slog.Logger
	now      func() time.Time
	backoffs []time.Duration
	jobs     chan Job
}

func NewNotifier(store DeliveryStore, catalogs lang.Catalogs, language func() lang.Code, logger *slog.Logger) *Notifier {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return egress.Dial(ctx, network, address, RequestTimeout)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: RequestTimeout,
		DisableKeepAlives:     true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Notifier{
		store: store, catalogs: catalogs, language: language, client: client, logger: logger,
		now: time.Now, backoffs: Backoffs, jobs: make(chan Job, queueSize),
	}
}

// SetBackoffs raccourcit les attentes, pour les tests.
func (n *Notifier) SetBackoffs(backoffs []time.Duration) {
	n.backoffs = backoffs
}

func (n *Notifier) SetClock(now func() time.Time) {
	n.now = now
}

// Enqueue prend la livraison sans jamais bloquer la source : une file
// pleine la laisse en attente en base, le démarrage suivant la rejouera.
func (n *Notifier) Enqueue(job Job) {
	select {
	case n.jobs <- job:
	default:
		n.logger.Warn("alert delivery queue full", "delivery_id", job.Delivery.ID, "channel_id", job.Channel.ID)
	}
}

// Run tient les ouvriers jusqu'à la fin du contexte.
func (n *Notifier) Run(ctx context.Context) {
	done := make(chan struct{})
	for range workerCount {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-n.jobs:
					n.process(ctx, job)
				}
			}
		}()
	}
	for range workerCount {
		<-done
	}
}

// process joue les essais d'une livraison et note l'issue en base.
func (n *Notifier) process(ctx context.Context, job Job) {
	body, err := n.render(job.Channel.Format, job.Delivery.Event, job.Alert)
	if err != nil {
		n.logger.Error("render alert body", "delivery_id", job.Delivery.ID, "error", err)
		n.finish(ctx, job.Delivery, Outcome{Reason: ReasonUnreachable})
		return
	}
	var outcome Outcome
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		if attempt > 0 && !n.wait(ctx, n.backoffs[min(attempt-1, len(n.backoffs)-1)]) {
			return
		}
		job.Delivery.Attempts++
		outcome = n.Send(ctx, job.Channel, job.Delivery.Event, body)
		if outcome.OK() {
			break
		}
		n.logger.Warn("alert delivery attempt failed", "delivery_id", job.Delivery.ID, "channel_id", job.Channel.ID,
			"attempt", job.Delivery.Attempts, "reason", string(outcome.Reason), "code", outcome.Code)
	}
	n.finish(ctx, job.Delivery, outcome)
}

func (n *Notifier) wait(ctx context.Context, delay time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func (n *Notifier) finish(ctx context.Context, delivery Delivery, outcome Outcome) {
	delivery.Status = DeliveryDelivered
	if !outcome.OK() {
		delivery.Status = DeliveryFailed
	}
	delivery.Reason = outcome.Reason
	delivery.Code = outcome.Code
	delivery.UpdatedAt = n.now()
	if err := n.store.UpdateDelivery(ctx, delivery); err != nil && ctx.Err() == nil {
		n.logger.Error("update alert delivery", "delivery_id", delivery.ID, "error", err)
	}
	if delivery.Status == DeliveryDelivered {
		n.logger.Info("alert delivered", "delivery_id", delivery.ID, "channel_id", delivery.ChannelID, "attempts", delivery.Attempts)
	}
}

func (n *Notifier) render(format Format, event Event, alert Alert) (Body, error) {
	return Render(n.catalogs.For(n.language()), format, event, alert, n.now())
}

// Test envoie un corps de test au canal, tout de suite et sans essai de
// plus : l'opérateur attend la réponse.
func (n *Notifier) Test(ctx context.Context, channel Channel) (Outcome, error) {
	body, err := n.render(channel.Format, EventTest, Alert{Severity: SeverityAttention})
	if err != nil {
		return Outcome{}, err
	}
	return n.Send(ctx, channel, EventTest, body), nil
}

// Send fait un envoi : POST du corps, signé par le secret du canal quand
// il y en a un. Un 2xx suffit ; tout autre code est un échec qui porte
// son code.
func (n *Notifier) Send(ctx context.Context, channel Channel, event Event, body Body) Outcome {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.URL, bytes.NewReader(body.Content))
	if err != nil {
		return Outcome{Reason: ReasonUnreachable}
	}
	request.Header.Set("Content-Type", body.ContentType)
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("OpenCloud-Event", string(event))
	if channel.Format == FormatText {
		// ntfy lit le titre et la priorité en en-tête ; les autres n'en font rien.
		request.Header.Set("Title", body.Title)
		request.Header.Set("Priority", priorityOf(event))
	}
	if channel.Secret != "" {
		request.Header.Set("OpenCloud-Signature", "sha256="+sign(channel.Secret, body.Content))
	}
	response, err := n.client.Do(request)
	if err != nil {
		return Outcome{Reason: reasonFor(err)}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBytes))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return Outcome{Code: response.StatusCode}
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return Outcome{Reason: ReasonRedirect, Code: response.StatusCode}
	}
	return Outcome{Reason: ReasonStatus, Code: response.StatusCode}
}

func priorityOf(event Event) string {
	if event == EventResolved || event == EventTest {
		return "default"
	}
	return "high"
}

// sign rend la signature HMAC-SHA256 du corps, en hexadécimal : le
// récepteur la recalcule avec le même secret.
func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify dit si une signature correspond au corps ; sert aux tests et à
// quiconque reçoit d'openCloud.
func Verify(secret string, body []byte, signature string) bool {
	return hmac.Equal([]byte("sha256="+sign(secret, body)), []byte(signature))
}

// reasonFor traduit une erreur de transport en un mot de la liste fermée.
func reasonFor(err error) Reason {
	var dnsErr *net.DNSError
	var tlsErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, egress.ErrForbiddenAddress):
		return ReasonForbidden
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return ReasonTimeout
	case errors.As(err, &dnsErr):
		return ReasonDNS
	case errors.As(err, &tlsErr):
		return ReasonTLS
	case errors.Is(err, syscall.ECONNREFUSED):
		return ReasonRefused
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ReasonTimeout
	}
	return ReasonUnreachable
}
