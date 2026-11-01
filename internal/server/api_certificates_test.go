package server

import (
	"net"
	"net/http"
	"testing"
)

func TestCertificateInspect_GivesTheFingerprintOfAnUntrustedAddressAndNeverTheKey(t *testing.T) {
	server, fake, _ := firewallBench(t)

	got := callAPI(server.Server, http.MethodPost, "/api/certificate/inspect", map[string]string{"url": fake.URL})
	if got.Code != http.StatusOK {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	var inspection certificateInspectionJSON
	decodeAPI(t, got, &inspection)
	if inspection.Outcome != "untrusted" || inspection.Fingerprint != fake.Pin() || inspection.NotAfter == nil {
		t.Fatalf("got %+v, want the fingerprint %s", inspection, fake.Pin())
	}
	// Aucun appel authentifié n'est parti : le faux serveur n'a vu que le handshake.
	if requests := fake.Requests(); len(requests) != 0 {
		t.Errorf("the inspection must send no HTTP request, saw %v", requests)
	}
}

func TestCertificateInspect_AnUnreachableAddressIsAnAnswerNotAnError(t *testing.T) {
	server := newTestServer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()

	got := callAPI(server.Server, http.MethodPost, "/api/certificate/inspect", map[string]string{"url": "https://" + address})
	var inspection certificateInspectionJSON
	decodeAPI(t, got, &inspection)
	if got.Code != http.StatusOK || inspection.Outcome != "unreachable" || inspection.Cause != "refused" || inspection.Fingerprint != "" {
		t.Fatalf("%d %+v", got.Code, inspection)
	}
}

func TestCertificateInspect_RefusesABadAddressAndClosesTheRouteToATokenButNotToTheOperator(t *testing.T) {
	server := newTestServer(t)
	bad := callAPI(server.Server, http.MethodPost, "/api/certificate/inspect", map[string]string{"url": "http://192.0.2.1"})
	if bad.Code != http.StatusUnprocessableEntity || errorCode(t, bad) != "certificate.url_invalid" {
		t.Fatalf("%d %s", bad.Code, bad.Body.String())
	}
	bearer := server.issueAPIToken(t, "mcp-maison")
	denied := callBearerBody(server.Server, http.MethodPost, "/api/certificate/inspect", bearer, `{"url":"https://192.0.2.1"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("a token must not open the route: %d %s", denied.Code, denied.Body.String())
	}
}
