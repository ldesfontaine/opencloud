package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestForbidden_LinkLocalAndMetadataAddresses(t *testing.T) {
	forbidden := []string{"169.254.169.254", "169.254.0.1", "fe80::1", "ff02::1", "ff01::1", "fd00:ec2::254", "100.100.100.200", "::ffff:100.100.100.200"}
	for _, raw := range forbidden {
		if !Forbidden(net.ParseIP(raw)) {
			t.Errorf("%s should be forbidden", raw)
		}
	}
	allowed := []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.20", "172.16.0.2", "fd00::1", "fd00:ec2::253", "100.64.0.1", "100.100.100.201", "93.184.216.34"}
	for _, raw := range allowed {
		if Forbidden(net.ParseIP(raw)) {
			t.Errorf("%s should be allowed", raw)
		}
	}
}

// Une adresse lien-local donnée en clair est refusée avant tout appel réseau.
func TestDial_RefusesLinkLocalLiteral(t *testing.T) {
	_, err := Dial(context.Background(), "tcp", "169.254.169.254:80", time.Second)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("err %v", err)
	}
}

// Les métadonnées d'AWS en IPv6 et d'Alibaba Cloud sont refusées comme
// celles du lien-local, avant tout appel réseau.
func TestDial_RefusesCloudMetadataOutsideLinkLocal(t *testing.T) {
	for _, address := range []string{"[fd00:ec2::254]:80", "100.100.100.200:80"} {
		_, err := Dial(context.Background(), "tcp", address, time.Second)
		if !errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("%s: err %v", address, err)
		}
	}
}

// La boucle locale reste ouverte : un service de la machine se joint.
func TestDial_ReachesLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := Dial(context.Background(), "tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestPrivate_LocalNetworkAddresses(t *testing.T) {
	local := []string{
		"127.0.0.1", "127.255.255.254", "::1",
		"10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"100.64.0.1", "100.100.100.201", "100.127.255.255",
		"fd00::1", "fc00::1", "fec0::1",
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::ffff:100.64.0.1",
		"0.0.0.0", "::",
	}
	for _, raw := range local {
		if !Private(net.ParseIP(raw)) {
			t.Errorf("%s should be a local network address", raw)
		}
	}
	public := []string{"172.32.0.1", "172.15.255.255", "100.63.255.255", "100.128.0.1", "8.8.8.8", "93.184.216.34", "2001:db8::1", "2606:4700::1111", "::ffff:8.8.8.8", "fbff::1"}
	for _, raw := range public {
		if Private(net.ParseIP(raw)) {
			t.Errorf("%s should not be a local network address", raw)
		}
	}
}

// Le lien-local et les métadonnées d'hébergeur restent interdits que le
// réseau local soit permis ou non : le droit probe.lan ne les ouvre pas.
func TestDialWith_NeverReachesForbiddenAddresses(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		for _, address := range []string{"169.254.169.254:80", "[fe80::1]:80", "[fd00:ec2::254]:80", "100.100.100.200:80", "[::ffff:169.254.169.254]:80"} {
			_, err := DialWith(context.Background(), "tcp", address, time.Second, allowPrivate)
			if !errors.Is(err, ErrForbiddenAddress) {
				t.Errorf("%s (allowPrivate=%v): err %v", address, allowPrivate, err)
			}
		}
	}
}

// Sans le droit, chaque forme du réseau local est refusée avant tout appel
// réseau, et le refus se distingue du lien-local.
func TestDialWith_RefusesLocalNetworkWithoutTheRight(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1:80", "[::1]:80", "10.1.2.3:80", "172.16.0.1:80", "192.168.1.1:80",
		"100.64.0.1:80", "[fd00::1]:80", "[::ffff:10.0.0.1]:80", "[::ffff:127.0.0.1]:80", "0.0.0.0:80", "[::]:80",
	} {
		_, err := DialWith(context.Background(), "tcp", address, time.Second, false)
		if !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("%s: err %v", address, err)
		}
		if errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("%s: the refusal must not read as a forbidden address", address)
		}
	}
}

// Un nom qui résout vers la boucle locale est refusé comme son adresse :
// la garde juge ce que le nom résout, pas ce qu'il dit.
func TestDialWith_JudgesWhatTheNameResolves(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := net.JoinHostPort("localhost", portOf(t, listener))

	if _, err := DialWith(context.Background(), "tcp", address, time.Second, false); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("without the right: err %v", err)
	}
	conn, err := DialWith(context.Background(), "tcp", address, time.Second, true)
	if err != nil {
		t.Fatalf("with the right: %v", err)
	}
	_ = conn.Close()
}

// Le serveur garde son comportement : Dial laisse le réseau local ouvert.
func TestDial_KeepsLocalNetworkOpenForTheServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := Dial(context.Background(), "tcp", net.JoinHostPort("localhost", portOf(t, listener)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func portOf(t *testing.T, listener net.Listener) string {
	t.Helper()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// Les formes exotiques d'une adresse locale : la garde juge l'adresse que la
// résolution rend, jamais le texte. Une IPv6 avec zone, une IPv4 dans IPv6
// écrite en hexadécimal ou en long, sont lues comme leur adresse et refusées ;
// le lien-local avec zone reste interdit, droit ou pas.
func TestDialWith_RefusesExoticLiteralForms(t *testing.T) {
	for _, address := range []string{
		"[::1%lo]:80", "[::ffff:7f00:1]:80", "[0:0:0:0:0:ffff:127.0.0.1]:80",
		"[::ffff:a00:1]:80", "[0000::0001]:80", "[fd12:3456::1%lo]:80",
	} {
		if _, err := DialWith(context.Background(), "tcp", address, time.Second, false); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("%s: err %v", address, err)
		}
	}
	for _, allowPrivate := range []bool{false, true} {
		if _, err := DialWith(context.Background(), "tcp", "[fe80::1%lo]:80", time.Second, allowPrivate); !errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("link-local with a zone (allowPrivate=%v): err %v", allowPrivate, err)
		}
	}
}

// Les écritures qu'inet_aton accepte (hexadécimal, octal, entier, octets
// omis) ne sont pas des adresses pour Go : elles partent au résolveur comme
// des noms. Celui de Go refuse d'emblée celles qui finissent par un nombre
// (« no such host ») ; les autres reçoivent ce que le DNS répond, ici la
// boucle locale, comme le ferait la libc, et la garde refuse ce qu'il rend.
// Aucune ne compose.
func TestDialWith_InetAtonFormsAreJudgedOnWhatTheyResolveTo(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := portOf(t, listener)
	withFakeDNS(t, []string{"127.0.0.1"})
	for _, host := range []string{"0x7f.0.0.1", "0x7f000001", "2130706433", "017700000001", "0177.0.0.1", "127.1", "127.0.1", "127.000.000.001"} {
		if net.ParseIP(host) != nil {
			t.Fatalf("%s is read as an address by Go: this test no longer covers it", host)
		}
		conn, err := DialWith(context.Background(), "tcp", net.JoinHostPort(host, port), time.Second, false)
		if err == nil {
			_ = conn.Close()
		}
		var notFound *net.DNSError
		if !errors.Is(err, ErrPrivateAddress) && !(errors.As(err, &notFound) && notFound.IsNotFound) {
			t.Errorf("%s: err %v", host, err)
		}
	}
}

// fakeDNS tient lieu de serveur DNS : chaque question A reçoit la réponse
// suivante de la liste (la dernière se répète), une question AAAA aucune.
// Il compte les questions A : un nom résolu deux fois pourrait répondre
// autre chose la seconde (rebinding).
type fakeDNS struct {
	mu      sync.Mutex
	answers [][]string
	asked   int
}

func (f *fakeDNS) next() []net.IP {
	f.mu.Lock()
	defer f.mu.Unlock()
	answer := f.answers[min(f.asked, len(f.answers)-1)]
	f.asked++
	ips := make([]net.IP, 0, len(answer))
	for _, raw := range answer {
		ips = append(ips, net.ParseIP(raw).To4())
	}
	return ips
}

func (f *fakeDNS) questions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked
}

// dial rend une connexion en flux (pas un PacketConn) : le résolveur de Go y
// parle DNS sur TCP, longueur sur deux octets puis le message.
func (f *fakeDNS) dial(_ context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	go f.serve(server)
	return client, nil
}

func (f *fakeDNS) serve(conn net.Conn) {
	defer conn.Close()
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return
	}
	query := make([]byte, int(size[0])<<8|int(size[1]))
	if _, err := io.ReadFull(conn, query); err != nil {
		return
	}
	// La question suit l'en-tête de 12 octets : des étiquettes jusqu'à
	// l'octet nul, puis le type et la classe.
	end := 12
	for end < len(query) && query[end] != 0 {
		end += int(query[end]) + 1
	}
	end += 5
	if end > len(query) {
		return
	}
	var ips []net.IP
	if query[end-4] == 0 && query[end-3] == 1 {
		ips = f.next()
	}
	// Réponse : même identifiant, QR RD RA, une question, len(ips) réponses.
	answer := []byte{query[0], query[1], 0x81, 0x80, 0, 1, 0, byte(len(ips)), 0, 0, 0, 0}
	answer = append(answer, query[12:end]...)
	for _, ip := range ips {
		answer = append(answer, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		answer = append(answer, ip...)
	}
	_, _ = conn.Write(append([]byte{byte(len(answer) >> 8), byte(len(answer))}, answer...))
}

// withFakeDNS fait passer le résolveur par défaut, celui de DialWith, par le
// faux serveur le temps du test.
func withFakeDNS(t *testing.T, answers ...[]string) *fakeDNS {
	t.Helper()
	fake := &fakeDNS{answers: answers}
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: fake.dial}
	t.Cleanup(func() { net.DefaultResolver = previous })
	return fake
}

// Le rebinding DNS ne passe pas : le nom est résolu une seule fois, la garde
// juge ces adresses-là, et la composition se fait sur l'adresse jugée, jamais
// sur le nom. Un nom qui répondrait autre chose à une seconde question n'en
// reçoit pas.
func TestDialWith_ResolvesOnceAndDialsWhatItJudged(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := net.JoinHostPort("rebind.opencloud.test", portOf(t, listener))

	// Sans le droit : la première réponse est la boucle locale, refusée ;
	// la seconde (publique) n'est jamais demandée.
	fake := withFakeDNS(t, []string{"127.0.0.1"}, []string{"192.0.2.1"})
	if _, err := DialWith(context.Background(), "tcp", address, time.Second, false); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("without the right: err %v", err)
	}
	if fake.questions() != 1 {
		t.Fatalf("the name was resolved %d times", fake.questions())
	}

	// Avec le droit : la composition va à l'adresse jugée (l'écouteur), pas à
	// ce qu'une seconde résolution aurait rendu (192.0.2.1, injoignable).
	fake = withFakeDNS(t, []string{"127.0.0.1"}, []string{"192.0.2.1"})
	conn, err := DialWith(context.Background(), "tcp", address, time.Second, true)
	if err != nil {
		t.Fatalf("with the right: %v", err)
	}
	_ = conn.Close()
	if fake.questions() != 1 {
		t.Fatalf("the name was resolved %d times", fake.questions())
	}

	// Un nom qui mêle boucle locale, privée et lien-local, dans un ordre ou
	// dans l'autre : refusé comme réseau local, sans rien composer.
	for _, answer := range [][]string{{"127.0.0.1", "10.0.0.1"}, {"169.254.169.254", "192.168.1.1"}, {"10.0.0.1", "169.254.169.254"}} {
		withFakeDNS(t, answer)
		if _, err := DialWith(context.Background(), "tcp", address, time.Second, false); !errors.Is(err, ErrPrivateAddress) {
			t.Errorf("%v: err %v", answer, err)
		}
	}
}
