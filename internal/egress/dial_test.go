package egress

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestForbidden_LinkLocalOnly(t *testing.T) {
	forbidden := []string{"169.254.169.254", "169.254.0.1", "fe80::1", "ff02::1", "ff01::1"}
	for _, raw := range forbidden {
		if !Forbidden(net.ParseIP(raw)) {
			t.Errorf("%s should be forbidden", raw)
		}
	}
	allowed := []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.20", "172.16.0.2", "fd00::1", "100.64.0.1", "93.184.216.34"}
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
