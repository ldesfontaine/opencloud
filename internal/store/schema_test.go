package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Le schéma vit dans migrations/001_schema.sql, écrit à la main pour être
// lu, puis dans les migrations qui le suivent. Ces tests en sont le filet :
// 001 est le même schéma que les 33 migrations qu'il remplace, élagage
// compris (les suivantes jouées de part et d'autre) ; ses clés étrangères
// visent des clés ; chaque table et chaque colonne sert au store.

const (
	legacyMigrationsDir = "testdata/legacy_migrations"
	legacyTrimFile      = "testdata/legacy_trim.sql"
)

// La 024 lisait temp.link_migration, que son étape Go remplissait des
// conteneurs à rattacher : sur une base vide, elle est vide.
const legacyLinkTable = `CREATE TEMP TABLE link_migration (
	container_id TEXT PRIMARY KEY, unit_id TEXT NOT NULL, service_id TEXT NOT NULL, match_level TEXT NOT NULL);`

// replayLegacy rejoue les anciennes migrations puis l'élagage, comme le
// runner les jouait : chacune hors clés étrangères, vérifiées avant le COMMIT.
// Les migrations venues après 001 suivent, comme sur une base montée.
func replayLegacy(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	db := openEmpty(t)
	entries, err := os.ReadDir(legacyMigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 33 {
		t.Fatalf("%d anciennes migrations, 33 attendues", len(entries))
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(legacyMigrationsDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		version := migrationVersion(entry.Name())
		script := string(content)
		if version == 24 {
			script = legacyLinkTable + script
		}
		if err := db.applyContent(ctx, entry.Name(), version, script); err != nil {
			t.Fatal(err)
		}
	}
	trim, err := os.ReadFile(legacyTrimFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.applyContent(ctx, filepath.Base(legacyTrimFile), 1000, string(trim)); err != nil {
		t.Fatal(err)
	}
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version > 1 {
			if err := db.apply(ctx, name, 1000+version); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

// 001_schema.sql donne, colonne par colonne, contrainte par contrainte et
// index par index, le schéma des 33 anciennes migrations suivies de
// l'élagage. On compare ce que SQLite dit du schéma, pas son texte : la
// mise en forme et l'ordre des colonnes changent, le sens non.
func TestSchema_MatchesTheLegacyMigrationsAndTheTrim(t *testing.T) {
	fresh := openTestDB(t)
	legacy := replayLegacy(t)

	want, got := describeSchema(t, legacy), describeSchema(t, fresh)

	for _, line := range want {
		if !slices.Contains(got, line) {
			t.Errorf("anciennes migrations seulement : %s", line)
		}
	}
	for _, line := range got {
		if !slices.Contains(want, line) {
			t.Errorf("001_schema.sql seulement : %s", line)
		}
	}
	if tables := count(t, fresh, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`); tables != 55 {
		t.Errorf("%d tables, 55 attendues (54 et schema_migrations)", tables)
	}
	versions, err := fresh.AppliedVersions(context.Background())
	if err != nil || !slices.Equal(versions, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 13}) {
		t.Fatalf("versions notées : %v %v", versions, err)
	}
}

// Une clé étrangère qui vise une colonne qui n'est ni la clé primaire ni une
// clé unique de son parent fait échouer chaque écriture (« foreign key
// mismatch ») ; SQLite ne le dit qu'à l'usage.
func TestSchema_ForeignKeysPointAtKeys(t *testing.T) {
	db := openTestDB(t)
	if violations := count(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); violations != 0 {
		t.Fatalf("%d clés étrangères pendantes sur une base neuve", violations)
	}
	for _, table := range schemaTables(t, db) {
		for _, key := range foreignKeys(t, db, table) {
			parentKeys := uniqueKeys(t, db, key.parent)
			if len(parentKeys) == 0 {
				t.Errorf("%s : le parent %s n'existe pas ou n'a pas de clé", table, key.parent)
				continue
			}
			if !slices.Contains(parentKeys, strings.Join(key.to, ",")) {
				t.Errorf("%s (%s) vise %s (%s), qui n'est pas une clé : %v",
					table, strings.Join(key.from, ","), key.parent, strings.Join(key.to, ","), parentKeys)
			}
		}
	}
}

// Le garde-fou contre le schéma mort : chaque table est lue et écrite par le
// store, et chaque colonne y est nommée, dans un fichier qui nomme sa table.
// Une table ou une colonne que plus rien ne touche se retire, ou ce test le
// dit.
func TestSchema_EveryTableAndColumnIsUsedByTheStore(t *testing.T) {
	db := openTestDB(t)
	sources := storeSources(t)
	all := strings.Join(sources, "\n")
	// DELETE FROM efface, il ne lit pas.
	readable := regexp.MustCompile(`(?i)DELETE\s+FROM`).ReplaceAllString(all, "DELETE")
	for _, table := range schemaTables(t, db) {
		if table == "schema_migrations" {
			continue
		}
		reads := regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+"?` + table + `\b`)
		writes := regexp.MustCompile(`(?i)\b(INTO|UPDATE)\s+"?` + table + `\b`)
		if !reads.MatchString(readable) {
			t.Errorf("table %s : aucune lecture dans le store", table)
		}
		if !writes.MatchString(all) {
			t.Errorf("table %s : aucune écriture dans le store", table)
		}
		var naming []string
		named := regexp.MustCompile(`\b` + table + `\b`)
		for _, source := range sources {
			if named.MatchString(source) {
				naming = append(naming, source)
			}
		}
		for _, column := range tableColumns(t, db, table) {
			if !regexp.MustCompile(`\b` + column + `\b`).MatchString(strings.Join(naming, "\n")) {
				t.Errorf("colonne %s.%s : nommée nulle part dans le store", table, column)
			}
		}
	}
}

// storeSources rend le Go du store, tests exclus.
func storeSources(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, string(content))
	}
	return sources
}

// describeSchema rend une ligne par fait du schéma, triées : options de
// chaque table, colonnes, contraintes CHECK, clés étrangères, index (ceux
// des contraintes UNIQUE et PRIMARY KEY compris), déclencheurs et vues.
func describeSchema(t *testing.T, db *DB) []string {
	t.Helper()
	var lines []string
	for _, table := range schemaTables(t, db) {
		if table == "schema_migrations" {
			continue
		}
		lines = append(lines, describeTable(t, db, table)...)
	}
	rows, err := db.sql.Query(`SELECT type, name, tbl_name, sql FROM sqlite_master WHERE type IN ('trigger', 'view')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name, table, body string
		if err := rows.Scan(&kind, &name, &table, &body); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("%s %s on %s: %s", kind, name, table, normalizeSQL(body)))
	}
	slices.Sort(lines)
	return lines
}

func describeTable(t *testing.T, db *DB, table string) []string {
	t.Helper()
	definition := stripComments(text(t, db, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table))
	upper := strings.ToUpper(definition)
	lines := []string{fmt.Sprintf("table %s without_rowid=%t autoincrement=%t",
		table, strings.Contains(upper, "WITHOUT ROWID"), strings.Contains(upper, "AUTOINCREMENT"))}

	columns, err := db.sql.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	for columns.Next() {
		var name, kind, defaultValue string
		var notNull, primaryKey int
		if err := columns.Scan(&name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("column %s.%s %s notnull=%d default=%s pk=%d",
			table, name, strings.ToUpper(kind), notNull, defaultValue, primaryKey))
	}
	columns.Close()

	for _, check := range tableChecks(definition) {
		lines = append(lines, fmt.Sprintf("check %s %s", table, check))
	}
	for _, key := range foreignKeys(t, db, table) {
		lines = append(lines, fmt.Sprintf("fk %s (%s) -> %s (%s) update=%s delete=%s match=%s",
			table, strings.Join(key.from, ","), key.parent, strings.Join(key.to, ","), key.onUpdate, key.onDelete, key.match))
	}
	for _, index := range tableIndexes(t, db, table) {
		lines = append(lines, fmt.Sprintf("index %s %s unique=%d (%s)%s",
			table, index.key, index.unique, strings.Join(index.columns, ", "), index.where))
	}
	return lines
}

func schemaTables(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.sql.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	return tables
}

func tableColumns(t *testing.T, db *DB, table string) []string {
	t.Helper()
	rows, err := db.sql.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	return columns
}

type foreignKey struct {
	parent                    string
	from, to                  []string
	onUpdate, onDelete, match string
}

// foreignKeys regroupe les colonnes de chaque clé étrangère, une clé
// composée tenant sur plusieurs lignes du pragma. Le parent est rendu sans
// guillemets : une table reconstruite peut l'avoir écrit entre guillemets.
func foreignKeys(t *testing.T, db *DB, table string) []foreignKey {
	t.Helper()
	rows, err := db.sql.Query(`SELECT id, "table", "from", COALESCE("to", ''), on_update, on_delete, "match"
		FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []foreignKey
	last := -1
	for rows.Next() {
		var id int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if id != last {
			keys = append(keys, foreignKey{parent: strings.Trim(parent, `"`), onUpdate: onUpdate, onDelete: onDelete, match: match})
			last = id
		}
		current := &keys[len(keys)-1]
		current.from = append(current.from, from)
		current.to = append(current.to, to)
	}
	return keys
}

type tableIndex struct {
	// Le nom d'un index déclaré ; « pk » ou « u » pour celui d'une
	// contrainte, dont SQLite tire le nom de la table.
	key     string
	unique  int
	columns []string
	where   string
}

func tableIndexes(t *testing.T, db *DB, table string) []tableIndex {
	t.Helper()
	rows, err := db.sql.Query(`SELECT name, "unique", origin, partial FROM pragma_index_list(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	type listed struct {
		name, origin    string
		unique, partial int
	}
	var all []listed
	for rows.Next() {
		var entry listed
		if err := rows.Scan(&entry.name, &entry.unique, &entry.origin, &entry.partial); err != nil {
			t.Fatal(err)
		}
		all = append(all, entry)
	}
	rows.Close()
	var indexes []tableIndex
	for _, entry := range all {
		index := tableIndex{key: entry.name, unique: entry.unique, columns: indexColumns(t, db, entry.name)}
		if entry.origin != "c" {
			index.key = entry.origin
		}
		if entry.partial == 1 {
			definition := normalizeSQL(text(t, db, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, entry.name))
			_, condition, _ := strings.Cut(definition, " WHERE ")
			index.where = " WHERE " + condition
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func indexColumns(t *testing.T, db *DB, index string) []string {
	t.Helper()
	rows, err := db.sql.Query(`SELECT COALESCE(name, '<expression>'), "desc", coll FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`, index)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name, collation string
		var descending int
		if err := rows.Scan(&name, &descending, &collation); err != nil {
			t.Fatal(err)
		}
		column := name
		if descending == 1 {
			column += " DESC"
		}
		if collation != "BINARY" {
			column += " COLLATE " + collation
		}
		columns = append(columns, column)
	}
	return columns
}

// uniqueKeys rend les clés d'une table, colonnes jointes par des virgules :
// sa clé primaire et ses index uniques complets. Une table sans clé primaire
// déclarée a son rowid, que rien ici ne vise.
func uniqueKeys(t *testing.T, db *DB, table string) []string {
	t.Helper()
	var keys []string
	var primary []string
	rows, err := db.sql.Query(`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`, table)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		primary = append(primary, name)
	}
	rows.Close()
	if len(primary) > 0 {
		keys = append(keys, strings.Join(primary, ","))
	}
	for _, index := range tableIndexes(t, db, table) {
		if index.unique == 1 && index.where == "" {
			keys = append(keys, strings.Join(index.columns, ","))
		}
	}
	return keys
}

var (
	sqlComment = regexp.MustCompile(`--[^\n]*`)
	checkWord  = regexp.MustCompile(`(?i)\bCHECK\s*\(`)
)

func stripComments(definition string) string {
	return sqlComment.ReplaceAllString(definition, "")
}

// normalizeSQL efface ce que la mise en forme change : commentaires, blancs,
// guillemets d'identifiant.
func normalizeSQL(definition string) string {
	flat := strings.Join(strings.Fields(stripComments(definition)), " ")
	flat = strings.ReplaceAll(flat, `"`, "")
	flat = strings.ReplaceAll(flat, "( ", "(")
	return strings.ReplaceAll(flat, " )", ")")
}

// tableChecks rend les expressions CHECK d'une définition de table, chacune
// avec ses parenthèses, triées.
func tableChecks(definition string) []string {
	var checks []string
	for _, at := range checkWord.FindAllStringIndex(definition, -1) {
		open := at[1] - 1
		depth := 0
		for end := open; end < len(definition); end++ {
			switch definition[end] {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				checks = append(checks, normalizeSQL(definition[open:end+1]))
				break
			}
		}
	}
	slices.Sort(checks)
	return checks
}
