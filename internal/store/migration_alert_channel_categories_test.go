package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// La 040 sur une base existante : un canal déjà là n'a aucune catégorie,
// donc reçoit tout, comme avant.
func TestMigration040_ExistingChannelsKeepReceivingEverything(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 40 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO alert_channels (id, name, url, format, min_severity, created_at) VALUES (1, 'ops', 'x', 'json', 'attention', 1)`)

	if err := db.apply(ctx, "040_alert_channel_categories.sql", 40); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.sql.QueryRowContext(ctx, `SELECT categories FROM alert_channels WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	channel := alert.Channel{Enabled: true, MinSeverity: alert.SeverityAttention, Categories: splitCategories(stored)}
	if stored != "" || len(channel.Categories) != 0 {
		t.Fatalf("an existing channel must have no category: %q", stored)
	}
	fact := alert.Alert{Kind: alert.KindAgentRolledBack, Severity: alert.SeverityAttention}
	if !channel.Wants(fact, alert.EventOpened) {
		t.Fatal("a channel without category receives everything")
	}
}

func channelOfID(t *testing.T, db *DB, id int64) alert.Channel {
	t.Helper()
	channel, err := db.GetChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func TestChannelCategories_RoundTripThroughTheStore(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	channel := alert.Channel{
		Name: "maj", URL: "https://hooks.example.org/x", Format: alert.FormatJSON, MinSeverity: alert.SeverityAttention, Enabled: true,
		Categories: []alert.Category{alert.CategoryBackups, alert.CategoryUpdates}, CreatedAt: time.Unix(1_800_000_000, 0),
	}
	id, err := db.InsertChannel(ctx, channel)
	if err != nil {
		t.Fatal(err)
	}
	if got := channelOfID(t, db, id).Categories; !slices.Equal(got, channel.Categories) {
		t.Fatalf("categories %v", got)
	}
	channel.ID, channel.Categories = id, nil
	if err := db.UpdateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if got := channelOfID(t, db, id).Categories; len(got) != 0 {
		t.Fatalf("emptied categories must read back empty: %v", got)
	}
}
