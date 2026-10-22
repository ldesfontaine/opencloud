package egress

import (
	"context"
	"errors"
	"net"
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
