package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

const (
	// Des faux secrets : le test cherche leur clair dans les fichiers de la base.
	plainMailPassword = "relay-password-8f3a"                                 // gitleaks:allow -- faux secret de test
	plainChannelURL   = "https://discord.example/api/webhooks/123/token-zq81" // gitleaks:allow -- faux secret de test
	plainChannelKey   = "hmac-key-77ab"                                       // gitleaks:allow -- faux secret de test
	plainPingToken    = "hb_7f9k2m4n6p8r0s2t4v6x8z"                           // gitleaks:allow -- faux secret de test
)

// openSecretsDB ouvre une base dont le sealer n'est pas encore posé.
func openSecretsDB(t *testing.T) *DB {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertPlaintextRows écrit en SQL brut des secrets en clair : un mot de
// passe, un canal, une tâche sans empreinte. Aucun chemin d'openCloud ne
// les écrit ainsi ; les tests s'assurent qu'ils ne sont pas pris pour des
// secrets.
func insertPlaintextRows(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO settings (id, mail_host, mail_password, updated_at) VALUES (1, 'smtp.example.org', ?, 1)`, []any{plainMailPassword}},
		{`INSERT INTO alert_channels (id, name, url, format, secret, min_severity, created_at) VALUES (7, 'chat', ?, 'discord', ?, 'attention', 1)`, []any{plainChannelURL, plainChannelKey}},
		{`INSERT INTO heartbeats (id, token, name, status, interval_seconds, grace_seconds, created_at) VALUES ('job1', ?, 'backup', 'new', 3600, 0, 1)`, []any{plainPingToken}},
	} {
		if _, err := db.sql.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// insertSealedRows écrit les mêmes lignes, chiffrées avec la clé posée,
// comme les écrit openCloud.
func insertSealedRows(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO settings (id, mail_host, mail_password, updated_at) VALUES (1, 'smtp.example.org', ?, 1)`, []any{mustSeal(t, db, plainMailPassword, settingsPasswordScope())}},
		{`INSERT INTO alert_channels (id, name, url, format, secret, min_severity, created_at) VALUES (7, 'chat', ?, 'discord', ?, 'attention', 1)`,
			[]any{mustSeal(t, db, plainChannelURL, channelScope("url", 7)), mustSeal(t, db, plainChannelKey, channelScope("secret", 7))}},
		{`INSERT INTO heartbeats (id, token, name, status, interval_seconds, grace_seconds, created_at) VALUES ('job1', '', 'backup', 'new', 3600, 0, 1)`, nil},
	} {
		if _, err := db.sql.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.writeHeartbeatToken(ctx, db.sql, "job1", plainPingToken); err != nil {
		t.Fatal(err)
	}
}

func mustSeal(t *testing.T, db *DB, plaintext string, scope seal.Scope) string {
	t.Helper()
	sealed, err := db.sealSecret(plaintext, scope)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func rawColumn(t *testing.T, db *DB, query string) string {
	t.Helper()
	var value string
	if err := db.sql.QueryRowContext(context.Background(), query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestVerifySealedSecrets_RefusesASealedValueCopiedFromAnotherRow(t *testing.T) {
	db := openSecretsDB(t)
	ctx := context.Background()
	db.SetSealer(seal.Generate())
	insertSealedRows(t, db)
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO alert_channels (id, name, url, format, min_severity, created_at) VALUES (8, 'other', ?, 'discord', 'attention', 1)`,
		mustSeal(t, db, "https://other.example/hook", channelScope("url", 8))); err != nil {
		t.Fatal(err)
	}
	if err := db.VerifySealedSecrets(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := db.sql.ExecContext(ctx, `UPDATE alert_channels SET url = (SELECT url FROM alert_channels WHERE id = 7) WHERE id = 8`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetChannel(ctx, 8); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("read of the copied value: got %v, want ErrSecretUnreadable", err)
	}
	err := db.VerifySealedSecrets(ctx)
	if !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("startup check: got %v, want ErrSecretUnreadable", err)
	}
	if want := "alert_channels.url row 8"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Fatalf("the error does not say where: %v", err)
	}
}

func TestVerifySealedSecrets_RefusesABaseSealedWithAnotherKey(t *testing.T) {
	db := openSecretsDB(t)
	ctx := context.Background()
	db.SetSealer(seal.Generate())
	insertSealedRows(t, db)
	if err := db.VerifySealedSecrets(ctx); err != nil {
		t.Fatal(err)
	}

	db.SetSealer(seal.Generate())
	if err := db.VerifySealedSecrets(ctx); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("got %v, want ErrSecretUnreadable", err)
	}
	if _, err := db.LoadSettings(ctx); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("settings read: got %v, want ErrSecretUnreadable", err)
	}
	if _, err := db.ListHeartbeats(ctx); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("heartbeats read: got %v, want ErrSecretUnreadable", err)
	}
}

func TestOpenSecret_RefusesAClearValueRead(t *testing.T) {
	db := openSecretsDB(t)
	insertPlaintextRows(t, db)
	db.SetSealer(seal.Generate())
	// Un clair n'est jamais rendu comme un secret.
	if _, err := db.LoadSettings(context.Background()); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("got %v, want ErrSecretUnreadable", err)
	}
}

func TestHasSealedSecrets(t *testing.T) {
	db := openSecretsDB(t)
	ctx := context.Background()
	has, err := db.HasSealedSecrets(ctx)
	if err != nil || has {
		t.Fatalf("empty base: %v, %v", has, err)
	}
	insertPlaintextRows(t, db)
	has, err = db.HasSealedSecrets(ctx)
	if err != nil || has {
		t.Fatalf("clear base: %v, %v", has, err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM settings; DELETE FROM alert_channels; DELETE FROM heartbeats`); err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	insertSealedRows(t, db)
	has, err = db.HasSealedSecrets(ctx)
	if err != nil || !has {
		t.Fatalf("sealed base: %v, %v", has, err)
	}
}

func TestWritesSealWhatTheyStore(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	id, err := db.InsertChannel(ctx, alert.Channel{
		Name: "chat", URL: plainChannelURL, Format: alert.FormatDiscord, Secret: plainChannelKey,
		MinSeverity: alert.SeverityAttention, NotifyResolve: true, Enabled: true, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertHeartbeat(ctx, heartbeat.Heartbeat{ID: "job1", Token: plainPingToken, Name: "backup", Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.MailPassword = plainMailPassword }); err != nil {
		t.Fatal(err)
	}

	for name, query := range map[string]string{
		"mail_password":  `SELECT mail_password FROM settings`,
		"channel url":    `SELECT url FROM alert_channels`,
		"channel secret": `SELECT secret FROM alert_channels`,
		"ping token":     `SELECT token FROM heartbeats`,
	} {
		if value := rawColumn(t, db, query); !seal.IsSealed(value) {
			t.Errorf("%s is stored in the clear: %q", name, value)
		}
	}
	if hash := rawColumn(t, db, `SELECT token_hash FROM heartbeats`); hash != heartbeat.HashToken(plainPingToken) {
		t.Errorf("token_hash %q is not the hash of the token", hash)
	}

	channel, err := db.GetChannel(ctx, id)
	if err != nil || channel.URL != plainChannelURL || channel.Secret != plainChannelKey {
		t.Fatalf("channel: %+v, %v", channel, err)
	}
	channel.URL = "https://hooks.example/new-token"
	channel.Secret = ""
	if err := db.UpdateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetChannel(ctx, id)
	if err != nil || updated.URL != channel.URL || updated.HasSecret {
		t.Fatalf("updated channel: %+v, %v", updated, err)
	}
	if got := rawColumn(t, db, `SELECT secret FROM alert_channels`); got != "" {
		t.Errorf("an empty secret must stay empty, got %q", got)
	}
}

func TestWithoutSealer_WritingASecretIsRefused(t *testing.T) {
	db := openSecretsDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	err := db.InsertHeartbeat(ctx, heartbeat.Heartbeat{ID: "job1", Token: plainPingToken, Name: "backup", Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: now})
	if !errors.Is(err, ErrNoSealer) {
		t.Fatalf("heartbeat: got %v, want ErrNoSealer", err)
	}
	_, err = db.InsertChannel(ctx, alert.Channel{Name: "chat", URL: plainChannelURL, Format: alert.FormatDiscord, MinSeverity: alert.SeverityAttention, CreatedAt: now})
	if !errors.Is(err, ErrNoSealer) {
		t.Fatalf("channel: got %v, want ErrNoSealer", err)
	}
	if channels, listErr := db.ListChannels(ctx); listErr != nil || len(channels) != 0 {
		t.Fatalf("a refused channel must leave no row: %d, %v", len(channels), listErr)
	}
	if _, err := db.UpdateSettings(ctx, func(current *settings.Settings) { current.MailPassword = plainMailPassword }); !errors.Is(err, ErrNoSealer) {
		t.Fatalf("settings: got %v, want ErrNoSealer", err)
	}
}

func TestForgetSecrets_EmptiesWhatWasLostAndRenewsPingTokens(t *testing.T) {
	db := openSecretsDB(t)
	ctx := context.Background()
	db.SetSealer(seal.Generate())
	insertSealedRows(t, db)
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO alert_channels (id, name, url, format, destination, min_severity, created_at) VALUES (9, 'mail', '', 'email', 'ops@example.org', 'attention', 1)`); err != nil {
		t.Fatal(err)
	}

	// La clé est perdue : on en pose une autre, puis on oublie.
	db.SetSealer(seal.Generate())
	channels, tokens, err := db.ForgetSecrets(ctx, heartbeat.NewToken)
	if err != nil {
		t.Fatal(err)
	}
	if channels != 1 || tokens != 1 {
		t.Fatalf("forgot %d channels and %d tokens, want 1 and 1", channels, tokens)
	}

	current, err := db.LoadSettings(ctx)
	if err != nil || current.MailPassword != "" || current.MailHost != "smtp.example.org" {
		t.Fatalf("settings: %+v, %v", current, err)
	}
	chat, err := db.GetChannel(ctx, 7)
	if err != nil || chat.URL != "" || chat.Secret != "" || chat.Enabled {
		t.Fatalf("lost channel: %+v, %v", chat, err)
	}
	mail, err := db.GetChannel(ctx, 9)
	if err != nil || !mail.Enabled || mail.Destination != "ops@example.org" {
		t.Fatalf("e-mail channel must be left alone: %+v, %v", mail, err)
	}
	if _, err := db.GetHeartbeatByToken(ctx, plainPingToken); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("old token: got %v, want ErrNotFound", err)
	}
	monitor, err := db.GetHeartbeat(ctx, "job1")
	if err != nil || monitor.Token == plainPingToken || monitor.Token == "" {
		t.Fatalf("renewed monitor: %+v, %v", monitor, err)
	}
	if found, err := db.GetHeartbeatByToken(ctx, monitor.Token); err != nil || found.ID != "job1" {
		t.Fatalf("new token: %+v, %v", found, err)
	}
	if err := db.VerifySealedSecrets(ctx); err != nil {
		t.Fatalf("the base must start after forgetting: %v", err)
	}

	again, tokensAgain, err := db.ForgetSecrets(ctx, heartbeat.NewToken)
	if err != nil || again != 0 || tokensAgain != 1 {
		t.Fatalf("replay: %d channels, %d tokens, %v", again, tokensAgain, err)
	}
}
