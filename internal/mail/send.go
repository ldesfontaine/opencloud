package mail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

// Les refus du relais, que l'appelant traduit en motif lisible. Une
// erreur réseau (délai, nom introuvable, adresse interdite) remonte telle
// quelle.
var (
	ErrNotConfigured = errors.New("mail: no relay configured")
	ErrAuthFailed    = errors.New("mail: relay refused the credentials")
	ErrRejected      = errors.New("mail: relay refused the message")
	ErrTLS           = errors.New("mail: tls with the relay failed")
)

// RelayError porte le code SMTP d'un refus, pour l'historique des envois.
type RelayError struct {
	Kind error
	Code int
	err  error
}

func (e *RelayError) Error() string {
	return e.Kind.Error() + ": " + e.err.Error()
}

func (e *RelayError) Unwrap() []error {
	return []error{e.Kind, e.err}
}

// Sender compose vers le relais par la garde de sortie, avec un délai
// borné pour tout l'échange.
type Sender struct {
	timeout time.Duration
	rootCAs *x509.CertPool
	now     func() time.Time
}

func NewSender(timeout time.Duration) *Sender {
	return &Sender{timeout: timeout, now: time.Now}
}

// SetRootCAs remplace les autorités du système, pour les tests.
func (s *Sender) SetRootCAs(pool *x509.CertPool) {
	s.rootCAs = pool
}

// Send livre le message aux destinataires par le relais.
func (s *Sender) Send(ctx context.Context, relay Relay, message Message) error {
	if !relay.Configured() {
		return ErrNotConfigured
	}
	content, err := Compose(relay.From, message, s.now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, err := s.dial(ctx, relay)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	// Le délai couvre tout le dialogue : un relais muet ne tient pas un ouvrier.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set deadline: %w", err)
		}
	}
	return s.converse(conn, relay, message.To, content)
}

func (s *Sender) dial(ctx context.Context, relay Relay) (net.Conn, error) {
	address := net.JoinHostPort(relay.Host, strconv.Itoa(relay.Port))
	conn, err := egress.Dial(ctx, "tcp", address, s.timeout)
	if err != nil {
		return nil, err
	}
	// En clair, le mot de passe ne quitte pas le réseau privé : on juge
	// l'adresse jointe, pas le nom, qui pourrait résoudre ailleurs.
	if relay.TLS == TLSNone && !isPrivateConn(conn) {
		_ = conn.Close()
		return nil, ErrPlainNotPrivate
	}
	if relay.TLS != TLSImplicit {
		return conn, nil
	}
	secured := tls.Client(conn, s.tlsConfig(relay.Host))
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, &RelayError{Kind: ErrTLS, err: err}
	}
	return secured, nil
}

func isPrivateConn(conn net.Conn) bool {
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	return ok && IsPrivate(remote.IP)
}

func (s *Sender) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: s.rootCAs}
}

// converse tient le dialogue SMTP : EHLO, STARTTLS exigé quand il est
// demandé, AUTH quand il y a un identifiant, puis l'enveloppe et le corps.
func (s *Sender) converse(conn net.Conn, relay Relay, recipients []string, content []byte) error {
	client, err := smtp.NewClient(conn, relay.Host)
	if err != nil {
		return classify(ErrRejected, err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Hello(heloName(relay.From)); err != nil {
		return classify(ErrRejected, err)
	}
	if relay.TLS == TLSStartTLS {
		if hasTLS, _ := client.Extension("STARTTLS"); !hasTLS {
			return &RelayError{Kind: ErrTLS, err: errors.New("relay does not offer STARTTLS")}
		}
		if err := client.StartTLS(s.tlsConfig(relay.Host)); err != nil {
			return &RelayError{Kind: ErrTLS, Code: smtpCode(err), err: err}
		}
	}
	if relay.Username != "" {
		if err := s.authenticate(client, relay); err != nil {
			return err
		}
	}
	return deliver(client, relay.From, recipients, content)
}

func (s *Sender) authenticate(client *smtp.Client, relay Relay) error {
	_, mechanisms := client.Extension("AUTH")
	auth := chooseAuth(strings.Fields(mechanisms), relay.Username, relay.Password)
	if auth == nil {
		return &RelayError{Kind: ErrAuthFailed, err: errors.New("relay offers neither PLAIN nor LOGIN")}
	}
	if err := client.Auth(auth); err != nil {
		return classify(ErrAuthFailed, err)
	}
	return nil
}

func deliver(client *smtp.Client, from string, recipients []string, content []byte) error {
	if err := client.Mail(from); err != nil {
		return classify(ErrRejected, err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return classify(ErrRejected, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return classify(ErrRejected, err)
	}
	if _, err := writer.Write(content); err != nil {
		return classify(ErrRejected, err)
	}
	if err := writer.Close(); err != nil {
		return classify(ErrRejected, err)
	}
	// Le message est accepté au point final de DATA : un QUIT raté n'y change rien.
	_ = client.Quit()
	return nil
}

// classify range une réponse du relais sous son genre ; une erreur de
// transport (délai, coupure) reste ce qu'elle est.
func classify(kind error, err error) error {
	var protocolErr *textproto.Error
	if !errors.As(err, &protocolErr) {
		return err
	}
	return &RelayError{Kind: kind, Code: protocolErr.Code, err: err}
}

func smtpCode(err error) int {
	var protocolErr *textproto.Error
	if errors.As(err, &protocolErr) {
		return protocolErr.Code
	}
	return 0
}

func heloName(from string) string {
	return from[strings.LastIndexByte(from, '@')+1:]
}

// chooseAuth prend PLAIN, sinon LOGIN, ce que proposent les relais
// courants. La connexion est déjà chiffrée, ou volontairement en clair
// sur un réseau privé : net/smtp.PlainAuth refuserait ce second cas.
func chooseAuth(mechanisms []string, username, password string) smtp.Auth {
	switch {
	case slices.Contains(mechanisms, "PLAIN"):
		return plainAuth{username: username, password: password}
	case slices.Contains(mechanisms, "LOGIN"):
		return loginAuth{username: username, password: password}
	}
	return nil
}

type plainAuth struct {
	username string
	password string
}

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

type loginAuth struct {
	username string
	password string
}

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

// Next répond aux deux questions de LOGIN, dans l'ordre où elles viennent.
func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if strings.HasPrefix(strings.ToLower(string(challenge)), "user") {
		return []byte(a.username), nil
	}
	return []byte(a.password), nil
}
