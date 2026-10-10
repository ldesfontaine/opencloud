package smtptest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Received est un message tel que le relais l'a reçu.
type Received struct {
	From       string
	Recipients []string
	Data       string
	// Secured : le message est arrivé sur une connexion chiffrée.
	Secured bool
	// Auth : l'identifiant accepté, vide sans authentification.
	Auth string
}

// Server est un faux relais. Username vide : il n'exige pas d'AUTH.
type Server struct {
	Addr     string
	Host     string
	Port     int
	Username string
	Password string
	// StartTLS : il propose STARTTLS ; Implicit : TLS dès la connexion.
	StartTLS bool
	Implicit bool
	// RejectRecipient : un RCPT TO vers cette adresse reçoit 550.
	RejectRecipient string
	// RootCAs reconnaît le certificat du relais.
	RootCAs *x509.CertPool

	listener  net.Listener
	tlsConfig *tls.Config
	mu        sync.Mutex
	received  []Received
}

// Options règle le faux relais avant qu'il n'écoute.
type Options struct {
	Username        string
	Password        string
	StartTLS        bool
	Implicit        bool
	RejectRecipient string
}

// Start écoute sur la boucle locale et s'arrête à la fin du test.
func Start(t *testing.T, options Options) *Server {
	t.Helper()
	server := &Server{
		Username: options.Username, Password: options.Password, StartTLS: options.StartTLS,
		Implicit: options.Implicit, RejectRecipient: options.RejectRecipient,
	}
	server.tlsConfig, server.RootCAs = selfSigned(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if server.Implicit {
		listener = tls.NewListener(listener, server.tlsConfig)
	}
	server.listener = listener
	server.Addr = listener.Addr().String()
	server.Host = "127.0.0.1"
	server.Port = listener.Addr().(*net.TCPAddr).Port
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// Messages rend ce qui a été reçu.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.received...)
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

// session est l'état d'une connexion.
type session struct {
	conn    net.Conn
	reader  *bufio.Reader
	secured bool
	auth    string
	current Received
}

func (s *Server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	state := &session{conn: conn, reader: bufio.NewReader(conn), secured: s.Implicit}
	state.reply("220 smtptest ready")
	for {
		line, err := state.reader.ReadString('\n')
		if err != nil {
			return
		}
		if !s.command(state, strings.TrimRight(line, "\r\n")) {
			return
		}
	}
}

// command répond à une ligne ; false ferme la connexion.
func (s *Server) command(state *session, line string) bool {
	verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
	switch verb {
	case "EHLO", "HELO":
		state.reply(s.ehlo(state)...)
	case "STARTTLS":
		return s.startTLS(state)
	case "AUTH":
		s.authenticate(state, line)
	case "MAIL":
		if s.Username != "" && state.auth == "" {
			state.reply("530 5.7.0 Authentication required")
			return true
		}
		state.current = Received{From: between(line, "<", ">"), Secured: state.secured, Auth: state.auth}
		state.reply("250 OK")
	case "RCPT":
		recipient := between(line, "<", ">")
		if recipient == s.RejectRecipient {
			state.reply("550 5.1.1 No such user")
			return true
		}
		state.current.Recipients = append(state.current.Recipients, recipient)
		state.reply("250 OK")
	case "DATA":
		s.data(state)
	case "QUIT":
		state.reply("221 Bye")
		return false
	default:
		state.reply("250 OK")
	}
	return true
}

func (s *Server) ehlo(state *session) []string {
	lines := []string{"250-smtptest"}
	if s.StartTLS && !state.secured {
		lines = append(lines, "250-STARTTLS")
	}
	lines = append(lines, "250-AUTH PLAIN LOGIN", "250 8BITMIME")
	return lines
}

func (s *Server) startTLS(state *session) bool {
	state.reply("220 Go ahead")
	secured := tls.Server(state.conn, s.tlsConfig)
	if err := secured.Handshake(); err != nil {
		return false
	}
	state.conn = secured
	state.reader = bufio.NewReader(secured)
	state.secured = true
	return true
}

func (s *Server) authenticate(state *session, line string) {
	fields := strings.Fields(line)
	var username, password string
	switch {
	case len(fields) == 3 && strings.EqualFold(fields[1], "PLAIN"):
		decoded, _ := base64.StdEncoding.DecodeString(fields[2])
		parts := strings.Split(string(decoded), "\x00")
		if len(parts) == 3 {
			username, password = parts[1], parts[2]
		}
	case len(fields) == 2 && strings.EqualFold(fields[1], "LOGIN"):
		username = state.ask("334 VXNlcm5hbWU6")
		password = state.ask("334 UGFzc3dvcmQ6")
	}
	if username == "" || username != s.Username || password != s.Password {
		state.reply("535 5.7.8 Authentication credentials invalid")
		return
	}
	state.auth = username
	state.reply("235 2.7.0 Authentication successful")
}

func (s *Server) data(state *session) {
	state.reply("354 End data with <CR><LF>.<CR><LF>")
	var data strings.Builder
	for {
		line, err := state.reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == ".\r\n" {
			break
		}
		data.WriteString(strings.TrimPrefix(line, "."))
	}
	state.current.Data = data.String()
	s.mu.Lock()
	s.received = append(s.received, state.current)
	s.mu.Unlock()
	state.reply("250 OK queued")
}

func (state *session) reply(lines ...string) {
	for _, line := range lines {
		_, _ = state.conn.Write([]byte(line + "\r\n"))
	}
}

// ask pose une question de LOGIN et rend la réponse décodée.
func (state *session) ask(question string) string {
	state.reply(question)
	line, err := state.reader.ReadString('\n')
	if err != nil {
		return ""
	}
	decoded, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	return string(decoded)
}

func between(line, opening, closing string) string {
	start := strings.Index(line, opening)
	end := strings.LastIndex(line, closing)
	if start < 0 || end <= start {
		return ""
	}
	return line[start+1 : end]
}

// selfSigned tire un certificat pour 127.0.0.1, et le trousseau qui le
// reconnaît.
func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtptest"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, pool
}
