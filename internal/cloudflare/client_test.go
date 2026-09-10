package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Les jetons des tests sont fabriqués, jamais écrits en dur : un scanner de
// secrets ne doit pas les prendre pour de vrais (packaging/test-action.sh fait
// de même pour son mot de passe).
func madeUpToken(what string) string {
	return "jetable-" + what + "-pour-le-test"
}

var testToken = madeUpToken("cloudflare")

// Les identifiants d'une zone et d'un enregistrement, tels que la
// documentation de Cloudflare les donne en exemple. Nommés une fois : écrits
// à côté d'un jeton, ils ressemblent à un secret pour un scanner.
const (
	testZoneID   = "023e105f4ecef8ad9ca31a8372d0c353"
	testRecordID = "372e67954025e0ba6aaa6d586b9e0b59"
)

// Les réponses réelles de l'API v4, recopiées de la documentation
// (https://developers.cloudflare.com/api/).
const (
	verifyReply = `{
	  "errors": [],
	  "messages": [],
	  "success": true,
	  "result": {
	    "id": "ed17574386854bf78a67040be0a770b0",
	    "status": "active",
	    "expires_on": "2020-01-01T00:00:00Z",
	    "not_before": "2018-07-01T05:20:00Z"
	  }
	}`
	zonesReply = `{
	  "success": true,
	  "errors": [],
	  "messages": [],
	  "result": [
	    {"id": "023e105f4ecef8ad9ca31a8372d0c353", "name": "exemple.fr", "status": "active"}
	  ],
	  "result_info": {"count": 1, "page": 1, "per_page": 20, "total_count": 1, "total_pages": 1}
	}`
	unauthorizedReply = `{
	  "success": false,
	  "errors": [{"code": 1000, "message": "Invalid API Token"}],
	  "messages": [],
	  "result": null
	}`
)

// recorded : ce que le faux Cloudflare a vu passer, pour éprouver qu'aucun
// jeton ne sort de l'en-tête.
type recorded struct {
	method        string
	path          string
	rawQuery      string
	authorization string
	body          string
}

// newFakeCloudflare répond selon le chemin demandé et note ce qu'il a reçu.
func newFakeCloudflare(t *testing.T, replies map[string]string) (*Client, *recorded) {
	t.Helper()

	seen := &recorded{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen.method, seen.path = r.Method, r.URL.Path
		seen.rawQuery, seen.authorization, seen.body = r.URL.RawQuery, r.Header.Get("Authorization"), string(body)

		reply, found := replies[r.URL.Path]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success": false, "errors": [{"code": 7003, "message": "Could not route to the endpoint"}]}`)
			return
		}
		if strings.Contains(reply, `"success": false`) {
			w.WriteHeader(http.StatusUnauthorized)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)

	return New(server.URL), seen
}

func TestVerifyToken_ActiveToken_PassesAndSendsTheTokenInTheHeaderOnly(t *testing.T) {
	client, seen := newFakeCloudflare(t, map[string]string{"/user/tokens/verify": verifyReply})

	if err := client.VerifyToken(context.Background(), testToken); err != nil {
		t.Fatalf("VerifyToken = %v, attendu nil", err)
	}

	if seen.method != http.MethodGet || seen.path != "/user/tokens/verify" {
		t.Errorf("appel %s %s, attendu GET /user/tokens/verify", seen.method, seen.path)
	}
	if seen.authorization != "Bearer "+testToken {
		t.Errorf("Authorization = %q", seen.authorization)
	}
	if strings.Contains(seen.rawQuery, testToken) || strings.Contains(seen.body, testToken) {
		t.Error("le jeton est sorti de l'en-tête : il est dans l'URL ou dans le corps")
	}
}

func TestVerifyToken_RefusedToken_SaysTheTokenIsInvalidWithoutQuotingIt(t *testing.T) {
	client, _ := newFakeCloudflare(t, map[string]string{"/user/tokens/verify": unauthorizedReply})

	err := client.VerifyToken(context.Background(), testToken)

	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("VerifyToken = %v, attendu ErrInvalidToken", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("l'erreur recopie le jeton : %q", err)
	}
}

// Un jeton expiré n'est pas refusé par le transport : l'API répond « success »
// avec un statut qui n'est pas « active ».
func TestVerifyToken_ExpiredToken_IsInvalidToo(t *testing.T) {
	expired := `{"success": true, "errors": [], "messages": [], "result": {"id": "ed17", "status": "expired"}}`
	client, _ := newFakeCloudflare(t, map[string]string{"/user/tokens/verify": expired})

	if err := client.VerifyToken(context.Background(), testToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("VerifyToken = %v, attendu ErrInvalidToken", err)
	}
}

func TestZoneByName_KnownZone_RendersItsIdentifier(t *testing.T) {
	client, seen := newFakeCloudflare(t, map[string]string{"/zones": zonesReply})

	zone, err := client.ZoneByName(context.Background(), testToken, "exemple.fr")
	if err != nil {
		t.Fatalf("ZoneByName = %v", err)
	}

	if zone.ID != testZoneID || zone.Name != "exemple.fr" {
		t.Errorf("zone = %+v", zone)
	}
	if seen.rawQuery != "name=exemple.fr" {
		t.Errorf("query = %q, attendu name=exemple.fr", seen.rawQuery)
	}
}

func TestZoneByName_AbsentZone_SaysZoneNotFound(t *testing.T) {
	empty := `{"success": true, "errors": [], "messages": [], "result": [], "result_info": {"count": 0}}`
	client, _ := newFakeCloudflare(t, map[string]string{"/zones": empty})

	_, err := client.ZoneByName(context.Background(), testToken, "absente.fr")

	if !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("ZoneByName = %v, attendu ErrZoneNotFound", err)
	}
}

// Le filtre de Cloudflare peut rendre des voisins : seul le nom exact compte.
func TestZoneByName_NeighbourZone_IsNotTheZoneAsked(t *testing.T) {
	neighbour := `{"success": true, "errors": [], "messages": [],
	  "result": [{"id": "023e", "name": "autre-exemple.fr", "status": "active"}]}`
	client, _ := newFakeCloudflare(t, map[string]string{"/zones": neighbour})

	if _, err := client.ZoneByName(context.Background(), testToken, "exemple.fr"); !errors.Is(err, ErrZoneNotFound) {
		t.Fatalf("ZoneByName = %v, attendu ErrZoneNotFound", err)
	}
}

func TestRecords_ListsWhatTheZoneHolds(t *testing.T) {
	reply := `{"success": true, "errors": [], "messages": [], "result": [
	  {"id": "372e67954025e0ba6aaa6d586b9e0b59", "zone_id": "023e105f4ecef8ad9ca31a8372d0c353",
	   "name": "_acme-challenge.exemple.fr", "type": "TXT", "content": "jeton-de-preuve", "ttl": 120, "proxied": false}
	]}`
	client, seen := newFakeCloudflare(t, map[string]string{"/zones/" + testZoneID + "/dns_records": reply})

	records, err := client.Records(context.Background(), testToken,
		testZoneID, "_acme-challenge.exemple.fr", "TXT")
	if err != nil {
		t.Fatalf("Records = %v", err)
	}

	if len(records) != 1 || records[0].Content != "jeton-de-preuve" || records[0].TTL != 120 {
		t.Fatalf("records = %+v", records)
	}
	if !strings.Contains(seen.rawQuery, "type=TXT") || !strings.Contains(seen.rawQuery, "name=_acme-challenge.exemple.fr") {
		t.Errorf("query = %q", seen.rawQuery)
	}
}

func TestCreateRecord_SendsTheRecordAndRendersWhatCloudflareCreated(t *testing.T) {
	reply := `{"success": true, "errors": [], "messages": [], "result":
	  {"id": "372e67954025e0ba6aaa6d586b9e0b59", "zone_id": "023e105f4ecef8ad9ca31a8372d0c353",
	   "name": "web.exemple.fr", "type": "A", "content": "192.0.2.10", "ttl": 300, "proxied": false}}`
	client, seen := newFakeCloudflare(t, map[string]string{"/zones/" + testZoneID + "/dns_records": reply})

	created, err := client.CreateRecord(context.Background(), testToken, testZoneID,
		Record{Name: "web.exemple.fr", Type: "A", Content: "192.0.2.10", TTL: 300})
	if err != nil {
		t.Fatalf("CreateRecord = %v", err)
	}

	if created.ID != testRecordID || created.Content != "192.0.2.10" {
		t.Fatalf("created = %+v", created)
	}
	if seen.method != http.MethodPost {
		t.Errorf("méthode = %s, attendu POST", seen.method)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(seen.body), &sent); err != nil {
		t.Fatalf("le corps envoyé n'est pas du JSON : %v", err)
	}
	if sent["type"] != "A" || sent["content"] != "192.0.2.10" || sent["proxied"] != false {
		t.Errorf("corps envoyé = %v", sent)
	}
	if _, present := sent["zone_id"]; present {
		t.Error("la zone est dans l'URL : elle n'a rien à faire dans le corps")
	}
}

func TestDeleteRecord_RemovesIt(t *testing.T) {
	reply := `{"success": true, "errors": [], "messages": [], "result": {"id": "372e67954025e0ba6aaa6d586b9e0b59"}}`
	path := "/zones/" + testZoneID + "/dns_records/" + testRecordID
	client, seen := newFakeCloudflare(t, map[string]string{path: reply})

	err := client.DeleteRecord(context.Background(), testToken, testZoneID, testRecordID)
	if err != nil {
		t.Fatalf("DeleteRecord = %v", err)
	}
	if seen.method != http.MethodDelete || seen.path != path {
		t.Errorf("appel %s %s, attendu DELETE %s", seen.method, seen.path, path)
	}
}

// Une erreur de Cloudflare se relit en clair : la phrase est la sienne.
func TestCall_CloudflareRefusal_QuotesItsOwnMessage(t *testing.T) {
	refused := `{"success": false, "errors": [{"code": 81044, "message": "Record does not exist"}], "messages": [], "result": null}`
	client, _ := newFakeCloudflare(t, map[string]string{"/zones/z/dns_records": refused})

	_, err := client.Records(context.Background(), testToken, "z", "", "")

	if err == nil || !strings.Contains(err.Error(), "Record does not exist") {
		t.Fatalf("Records = %v, attendu la phrase de Cloudflare", err)
	}
}

// Le réseau tombe : l'appel rend une erreur, il ne bloque pas.
func TestCall_NetworkFailure_IsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	client := New(address)

	if err := client.VerifyToken(context.Background(), testToken); err == nil {
		t.Fatal("VerifyToken = nil, attendu une erreur de réseau")
	}
}

// Un serveur qui ne répond jamais : le délai du client borne l'attente, et le
// contexte annulé la borne encore avant.
func TestCall_ServerThatNeverAnswers_StopsOnTheContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := New(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := client.VerifyToken(ctx, testToken)

	if err == nil {
		t.Fatal("VerifyToken = nil, attendu un délai dépassé")
	}
	if waited := time.Since(started); waited > requestTimeout {
		t.Errorf("l'appel a attendu %s, plus que le délai du client", waited)
	}
}

// Un portail captif répond du HTML : ce n'est pas l'API, et on le dit.
func TestCall_ReplyThatIsNotJSON_SaysTheReplyIsUnreadable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html>portail captif</html>")
	}))
	t.Cleanup(server.Close)

	err := New(server.URL).VerifyToken(context.Background(), testToken)

	if !errors.Is(err, ErrUnreadableReply) {
		t.Fatalf("VerifyToken = %v, attendu ErrUnreadableReply", err)
	}
}
