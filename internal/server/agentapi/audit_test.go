package agentapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// L'enrôlement est le seul geste d'un agent qui écrit un objet déclaré : il
// va au journal sous la machine qu'il crée.
func TestAgentEnroll_IsJournaledUnderTheAgent(t *testing.T) {
	server := newBench(t)
	server.machines.SetAuditor(audit.New(server.db, server.logger))
	cleartext, _, err := server.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	public, _, _ := ed25519.GenerateKey(nil)
	request := agentwire.EnrollRequest{MachineID: remoteID, PublicKey: base64.StdEncoding.EncodeToString(public), Token: cleartext, Hostname: "vps"}
	if recorder := server.postJSON("/agent/enroll", request); recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != audit.MachineEnroll || events[0].Actor != audit.Agent(remoteID) || events[0].ObjectName != "vps-paris-1" {
		t.Fatalf("events: %+v", events)
	}
}
