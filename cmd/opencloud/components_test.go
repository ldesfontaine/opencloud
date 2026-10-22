package main

import (
	"log/slog"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Le câblage commun suffit à construire un serveur, une fois le direct
// posé : c'est ce que fait serve.
func TestBuildComponents_ServerStarts(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	cfg := config.Default()
	parts, err := buildComponents(cfg, root, db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	options := parts.serverOptions(cfg)
	options.Live = live.New()
	options.PublicLive = live.New()
	if _, err := server.New(options); err != nil {
		t.Fatalf("server.New: %v", err)
	}
}

// serve prend ses options du serveur à un seul endroit : chaque composant y
// est posé, et il ne reste à l'appelant que le direct et le
// secret du mandataire. Un composant ajouté au serveur sans passer par
// buildComponents se voit ici.
func TestBuildComponents_OptionsLeaveOnlyLiveToCaller(t *testing.T) {
	root, db := storetest.OpenWithStateDir(t)
	cfg := config.Default()
	cfg.MCPURL = "http://127.0.0.1:3000"
	parts, err := buildComponents(cfg, root, db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	options := parts.serverOptions(cfg)
	posed := map[string]bool{
		"Logger":     options.Logger != nil,
		"Reader":     options.Reader != nil,
		"Settings":   options.Settings != nil,
		"Machines":   options.Machines != nil,
		"Heartbeats": options.Heartbeats != nil,
		"Resources":  options.Resources != nil,
		"Containers": options.Containers != nil,
		"Probes":     options.Probes != nil,
		"Directory":  options.Directory != nil,
		"Manifest":   options.Manifest != nil,
		"Updates":    options.Updates != nil,
		"Lifecycle":  options.Lifecycle != nil,
		"Status":     options.Status != nil,
		"Alerts":     options.Alerts != nil,
		"Notifier":   options.Notifier != nil,
		"APITokens":  options.APITokens != nil,
		"Auth":       options.Auth != nil,
		"Version":    options.Version != "",
		"Listen":     options.Listen == cfg.Listen,
		"MCPURL":     options.MCPURL == cfg.MCPURL,
	}
	for name, isPosed := range posed {
		if !isPosed {
			t.Errorf("%s n'est pas posé par le câblage commun", name)
		}
	}
	if options.Live != nil || options.PublicLive != nil || options.ForwardSecret != "" || options.Clock != nil {
		t.Fatal("le direct, le secret du mandataire et l'horloge restent à l'appelant")
	}
}
