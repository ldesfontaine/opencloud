package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

var (
	// ErrNoSealer : un secret allait être écrit ou lu sans que la clé du
	// serveur soit donnée à la base. C'est un oubli du démarrage.
	ErrNoSealer = errors.New("store: no sealer set, cannot read or write secrets")
	// ErrSecretUnreadable : un secret en base n'est pas un chiffré, ou ne
	// s'ouvre pas avec la clé du serveur (autre clé, valeur altérée, copiée
	// d'une autre ligne).
	ErrSecretUnreadable = errors.New("store: secret cannot be read")
)

// Sealer chiffre et déchiffre les secrets d'une ligne ; seal.Keyring le
// satisfait.
type Sealer interface {
	Seal(plaintext string, scope seal.Scope) string
	Open(sealed string, scope seal.Scope) (string, error)
}

// SetSealer donne à la base la clé du serveur. Il se pose au démarrage,
// avant toute lecture ou écriture d'un secret : la base n'en protège pas
// l'accès.
func (db *DB) SetSealer(sealer Sealer) {
	db.sealer = sealer
}

// Les colonnes qui portent un secret. Chacune se lie à sa ligne par la
// valeur de sa clé (id, sauf mention), convertie en texte.
type secretColumn struct {
	table  string
	column string
	// keyColumn est la clé de la table quand elle n'a pas d'id.
	keyColumn string
}

func (target secretColumn) key() string {
	if target.keyColumn == "" {
		return "id"
	}
	return target.keyColumn
}

var secretColumns = []secretColumn{
	{table: "settings", column: "mail_password"},
	{table: "alert_channels", column: "url"},
	{table: "alert_channels", column: "secret"},
	{table: "heartbeats", column: "token_sealed"},
	{table: "machine_proxmox", column: "token_secret", keyColumn: "machine_id"},
	{table: "firewalls", column: "api_secret"},
}

func settingsPasswordScope() seal.Scope {
	// La table des réglages n'a qu'une ligne, d'id 1.
	return seal.Scope{Table: "settings", Column: "mail_password", Row: "1"}
}

func channelScope(column string, id int64) seal.Scope {
	return seal.Scope{Table: "alert_channels", Column: column, Row: strconv.FormatInt(id, 10)}
}

func heartbeatTokenScope(id string) seal.Scope {
	return seal.Scope{Table: "heartbeats", Column: "token_sealed", Row: id}
}

func firewallSecretScope(id string) seal.Scope {
	return seal.Scope{Table: "firewalls", Column: "api_secret", Row: id}
}

func proxmoxTokenScope(machineID string) seal.Scope {
	return seal.Scope{Table: "machine_proxmox", Column: "token_secret", Row: machineID}
}

// sealSecret chiffre un secret pour sa ligne. Un secret vide reste vide : il
// n'y a rien à cacher, et la colonne vide dit « pas réglé ».
func (db *DB) sealSecret(plaintext string, scope seal.Scope) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if db.sealer == nil {
		return "", ErrNoSealer
	}
	return db.sealer.Seal(plaintext, scope), nil
}

// openSecret rend le clair d'un secret lu en base. Une valeur non vide qui
// n'est pas un chiffré est refusée, jamais rendue telle quelle : ce serait
// prendre un clair oublié pour un secret en règle.
func (db *DB) openSecret(stored string, scope seal.Scope) (string, error) {
	if stored == "" {
		return "", nil
	}
	if db.sealer == nil {
		return "", ErrNoSealer
	}
	if !seal.IsSealed(stored) {
		return "", unreadable(scope, seal.ErrMalformed)
	}
	plaintext, err := db.sealer.Open(stored, scope)
	if err != nil {
		return "", unreadable(scope, err)
	}
	return plaintext, nil
}

func unreadable(scope seal.Scope, cause error) error {
	return fmt.Errorf("%w: %s.%s row %s: %w", ErrSecretUnreadable, scope.Table, scope.Column, scope.Row, cause)
}

// HasSealedSecrets dit si la base porte au moins un secret chiffré. Sans
// clé, c'est ce qui interdit d'en créer une neuve : elle chiffrerait les
// écritures suivantes et la base mêlerait deux clés.
func (db *DB) HasSealedSecrets(ctx context.Context) (bool, error) {
	for _, target := range secretColumns {
		stored, err := readSecretColumn(ctx, db.sql, target)
		if err != nil {
			return false, err
		}
		for _, entry := range stored {
			if seal.IsSealed(entry.value) {
				return true, nil
			}
		}
	}
	return false, nil
}

type storedSecret struct {
	row   string
	value string
}

// readSecretColumn lit les valeurs non vides d'une colonne, avec leur ligne.
func readSecretColumn(ctx context.Context, from querier, target secretColumn) ([]storedSecret, error) {
	// Table et colonne viennent de secretColumns, jamais d'une saisie.
	query := `SELECT CAST(` + target.key() + ` AS TEXT), ` + target.column + ` FROM ` + target.table + ` WHERE ` + target.column + ` <> ''` // #nosec G202 -- constantes du paquet.
	rows, err := from.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read %s.%s: %w", target.table, target.column, err)
	}
	return collect(rows, func(row scanner) (storedSecret, error) {
		var entry storedSecret
		if err := row.Scan(&entry.row, &entry.value); err != nil {
			return storedSecret{}, fmt.Errorf("scan %s.%s: %w", target.table, target.column, err)
		}
		return entry, nil
	})
}

// VerifySealedSecrets ouvre chaque secret chiffré de la base avec la clé du
// serveur, sans rien écrire : un secret qui ne s'ouvre pas rend
// ErrSecretUnreadable. Les clairs restés ne comptent pas, ils ne sont pas des
// chiffrés.
func (db *DB) VerifySealedSecrets(ctx context.Context) error {
	if db.sealer == nil {
		return ErrNoSealer
	}
	for _, target := range secretColumns {
		stored, err := readSecretColumn(ctx, db.sql, target)
		if err != nil {
			return err
		}
		for _, entry := range stored {
			if !seal.IsSealed(entry.value) {
				continue
			}
			scope := seal.Scope{Table: target.table, Column: target.column, Row: entry.row}
			if _, err := db.openSecret(entry.value, scope); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResealSecrets rescelle chaque secret de la base avec la clé qui scelle,
// dans une seule transaction : le temps d'une rotation, le Sealer ouvre avec
// l'ancienne clé et scelle avec la neuve. Un secret qui ne s'ouvre pas
// annule tout. Rend le nombre de secrets rescellés.
func (db *DB) ResealSecrets(ctx context.Context) (int, error) {
	if db.sealer == nil {
		return 0, ErrNoSealer
	}
	resealed := 0
	err := db.inTx(ctx, "resealing secrets", func(tx *sql.Tx) error {
		for _, target := range secretColumns {
			stored, err := readSecretColumn(ctx, tx, target)
			if err != nil {
				return err
			}
			for _, entry := range stored {
				if err := db.resealOne(ctx, tx, target, entry); err != nil {
					return err
				}
				resealed++
			}
		}
		return nil
	})
	return resealed, err
}

func (db *DB) resealOne(ctx context.Context, tx *sql.Tx, target secretColumn, entry storedSecret) error {
	scope := seal.Scope{Table: target.table, Column: target.column, Row: entry.row}
	plaintext, err := db.openSecret(entry.value, scope)
	if err != nil {
		return err
	}
	sealed, err := db.sealSecret(plaintext, scope)
	if err != nil {
		return err
	}
	// Table et colonne viennent de secretColumns, jamais d'une saisie.
	query := `UPDATE ` + target.table + ` SET ` + target.column + ` = ? WHERE CAST(` + target.key() + ` AS TEXT) = ?` // #nosec G202 -- constantes du paquet.
	if _, err := tx.ExecContext(ctx, query, sealed, entry.row); err != nil {
		return fmt.Errorf("reseal %s.%s: %w", target.table, target.column, err)
	}
	return nil
}

// writeHeartbeatToken pose le jeton d'une tâche, chiffré, avec l'empreinte
// par laquelle un ping le retrouve : les deux colonnes s'écrivent ensemble
// ou pas du tout.
func (db *DB) writeHeartbeatToken(ctx context.Context, to execer, id, token string) (sql.Result, error) {
	sealed, err := db.sealSecret(token, heartbeatTokenScope(id))
	if err != nil {
		return nil, fmt.Errorf("seal heartbeat token: %w", err)
	}
	return to.ExecContext(ctx, `UPDATE heartbeats SET token_sealed = ?, token_lookup = ? WHERE id = ?`,
		sealed, heartbeat.HashToken(token), id)
}

// Forgotten dit ce que ForgetSecrets a vidé.
type Forgotten struct {
	// Channels : les canaux désactivés.
	Channels int
	// PingTokens : les jetons de tâche renouvelés.
	PingTokens int
	// ProxmoxHosts : les hôtes Proxmox dont le jeton est à ressaisir.
	ProxmoxHosts int
	// Firewalls : les pare-feu dont le secret d'API est à ressaisir.
	Firewalls int
}

// ForgetSecrets est la sortie de secours d'une clé perdue : ce qui était
// chiffré avec elle est irrécupérable, la base est remise dans un état qui
// démarre. Le mot de passe du relais est vidé ; chaque canal qui portait une
// URL ou un secret est vidé et désactivé ; chaque tâche reçoit un nouveau
// jeton ; chaque hôte Proxmox et chaque pare-feu perd son secret et reste déclaré, son
// prochain relevé dira « refusé » jusqu'à ce qu'on le redéclare. Le Sealer doit
// déjà être celui de la nouvelle clé.
func (db *DB) ForgetSecrets(ctx context.Context, newToken func() (string, error)) (Forgotten, error) {
	if db.sealer == nil {
		return Forgotten{}, ErrNoSealer
	}
	var forgotten Forgotten
	err := db.inTx(ctx, "forgetting secrets", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET mail_password = ''`); err != nil {
			return fmt.Errorf("forget mail password: %w", err)
		}
		// Un canal e-mail n'a ni URL ni secret : il n'a rien perdu, il reste actif.
		result, err := tx.ExecContext(ctx,
			`UPDATE alert_channels SET url = '', secret = '', enabled = 0 WHERE url <> '' OR secret <> ''`)
		if err != nil {
			return fmt.Errorf("forget channel secrets: %w", err)
		}
		forgotten.Channels, err = countAffected(result)
		if err != nil {
			return fmt.Errorf("count forgotten channels: %w", err)
		}
		forgotten.PingTokens, err = db.renewHeartbeatTokens(ctx, tx, newToken)
		if err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx, `UPDATE machine_proxmox SET token_secret = '' WHERE token_secret <> ''`)
		if err != nil {
			return fmt.Errorf("forget proxmox token secrets: %w", err)
		}
		forgotten.ProxmoxHosts, err = countAffected(result)
		if err != nil {
			return fmt.Errorf("count forgotten proxmox hosts: %w", err)
		}
		result, err = tx.ExecContext(ctx, `UPDATE firewalls SET api_secret = '' WHERE api_secret <> ''`)
		if err != nil {
			return fmt.Errorf("forget firewall secrets: %w", err)
		}
		forgotten.Firewalls, err = countAffected(result)
		if err != nil {
			return fmt.Errorf("count forgotten firewalls: %w", err)
		}
		return nil
	})
	if err != nil {
		return Forgotten{}, err
	}
	return forgotten, nil
}

func countAffected(result sql.Result) (int, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil // #nosec G115 -- au plus le nombre de lignes d'une table de la base.
}

func (db *DB) renewHeartbeatTokens(ctx context.Context, tx *sql.Tx, newToken func() (string, error)) (int, error) {
	ids, err := listHeartbeatIDs(ctx, tx)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		token, err := newToken()
		if err != nil {
			return 0, fmt.Errorf("draw heartbeat token: %w", err)
		}
		if _, err := db.writeHeartbeatToken(ctx, tx, id, token); err != nil {
			return 0, fmt.Errorf("renew heartbeat token: %w", err)
		}
	}
	return len(ids), nil
}

// Les identifiants se lisent d'abord, rows refermées, puis les jetons
// s'écrivent : une transaction n'a qu'une connexion.
func listHeartbeatIDs(ctx context.Context, from querier) ([]string, error) {
	rows, err := from.QueryContext(ctx, `SELECT id FROM heartbeats`)
	if err != nil {
		return nil, fmt.Errorf("list heartbeat ids: %w", err)
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		if err := row.Scan(&id); err != nil {
			return "", fmt.Errorf("scan heartbeat id: %w", err)
		}
		return id, nil
	})
}
