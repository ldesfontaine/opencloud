package tlsinspect

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

func TestInspect_ASelfSignedCertificateIsUntrustedAndGivesItsFingerprint(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()

	report, err := Inspect(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(server.Certificate().Raw)
	if report.Outcome != Untrusted || report.Fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatalf("got %+v", report)
	}
	if report.NotAfter.IsZero() {
		t.Error("the expiry is missing")
	}
}

func TestInspect_ACertificateTheSystemKnowsIsTrusted(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	trustedRoots = x509.NewCertPool()
	trustedRoots.AddCert(server.Certificate())
	defer func() { trustedRoots = nil }()

	report, err := Inspect(context.Background(), server.URL)
	if err != nil || report.Outcome != Trusted || report.Fingerprint == "" {
		t.Fatalf("got %+v, %v", report, err)
	}
}

func TestInspect_NothingListeningIsRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	report, err := Inspect(context.Background(), "https://"+address)
	if err != nil || report.Outcome != Unreachable || report.Cause != CauseRefused {
		t.Fatalf("got %+v, %v", report, err)
	}
	if report.Fingerprint != "" {
		t.Error("an unreachable address gives no fingerprint")
	}
}

func TestInspect_APlainHTTPPortIsNotTLS(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	report, err := Inspect(context.Background(), "https://"+strings.TrimPrefix(server.URL, "http://"))
	if err != nil || report.Outcome != Unreachable || report.Cause != CauseNotTLS {
		t.Fatalf("got %+v, %v", report, err)
	}
}

func TestInspect_ASilentPortIsATimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Le port accepte puis se tait : le handshake attend jusqu'au délai du contexte.
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			time.Sleep(time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	report, err := Inspect(ctx, "https://"+listener.Addr().String())
	if err != nil || report.Outcome != Unreachable || report.Cause != CauseTimeout {
		t.Fatalf("got %+v, %v", report, err)
	}
}

func TestInspect_ALinkLocalAddressIsForbidden(t *testing.T) {
	report, err := Inspect(context.Background(), "https://169.254.169.254")
	if err != nil || report.Outcome != Unreachable || report.Cause != CauseForbidden {
		t.Fatalf("got %+v, %v", report, err)
	}
}

func TestInspect_RefusesWhatIsNotAnHTTPSHostAddress(t *testing.T) {
	for _, raw := range []string{"", "192.0.2.1", "http://192.0.2.1", "https://", "https://user@192.0.2.1", "https://192.0.2.1/ui", "https://192.0.2.1?x=1"} {
		_, err := Inspect(context.Background(), raw)
		if !errors.Is(err, ErrURLInvalid) {
			t.Errorf("%q : %v", raw, err)
		}
	}
	if key, ok := refusal.KeyOf(ErrURLInvalid); !ok || key != "certificate.url_invalid" {
		t.Errorf("key %q", key)
	}
}

func TestSplitAddress_DefaultsToPort443(t *testing.T) {
	host, port, err := splitAddress("https://[fd00::2]/")
	if err != nil || host != "fd00::2" || port != "443" {
		t.Fatalf("%q %q %v", host, port, err)
	}
}
