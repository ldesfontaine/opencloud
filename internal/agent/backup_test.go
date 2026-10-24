package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const (
	parcelSession = "session-de-test"
	archiveName   = "opencloud-20261021-030000.tar.age"
	keyName       = "opencloud-key-0123456789abcdef.age"
)

// Un faux contenu age : l'en-tête suffit à l'agent, qui n'a pas la clé.
func ageLike(text string) string {
	return ageHeader + text
}

// parcelServer sert un colis et exige le jeton de session et la version du
// protocole, comme la route du serveur. Il reçoit aussi un fichier téléversé
// (PUT) dans le colis « colis1 » et le garde, sommes vérifiées.
type parcelServer struct {
	files    map[string]string
	requests []string

	mu sync.Mutex
	// Ce que les PUT ont livré, par nom ; et ce que le serveur en a vu.
	uploaded map[string][]byte
	puts     []putRecord
	// putAnswers donne, dans l'ordre des PUT, ce que le serveur fait : 0 le
	// traite, un code HTTP le répond sans lire le corps, -1 coupe la connexion.
	putAnswers []int
}

// putRecord est ce qu'un PUT a montré au serveur.
type putRecord struct {
	name          string
	contentLength int64
	sum           string
	expect        string
}

func (p *parcelServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/parcels/{parcel}/{name}", func(w http.ResponseWriter, r *http.Request) {
		p.requests = append(p.requests, r.PathValue("parcel")+"/"+r.PathValue("name"))
		if r.Header.Get(agentwire.HeaderProtocol) != agentwire.ProtocolVersion || r.Header.Get("Authorization") != "Bearer "+parcelSession {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		content, found := p.files[r.PathValue("name")]
		if !found || r.PathValue("parcel") != "colis1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(content))
	})
	mux.HandleFunc("PUT /agent/parcels/{parcel}/{name}", p.put)
	return mux
}

func (p *parcelServer) put(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	attempt := len(p.puts)
	p.puts = append(p.puts, putRecord{
		name: r.PathValue("name"), contentLength: r.ContentLength,
		sum: r.Header.Get(agentwire.HeaderParcelSHA256), expect: r.Header.Get("Expect"),
	})
	answer := 0
	if attempt < len(p.putAnswers) {
		answer = p.putAnswers[attempt]
	}
	p.mu.Unlock()
	switch {
	case answer < 0:
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	case answer > 0:
		w.WriteHeader(answer)
		_, _ = w.Write([]byte(`{"error":"refused"}`))
		return
	}
	if r.Header.Get(agentwire.HeaderProtocol) != agentwire.ProtocolVersion || r.Header.Get("Authorization") != "Bearer "+parcelSession {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.PathValue("parcel") != "colis1" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	content, err := io.ReadAll(r.Body)
	sum := sha256.Sum256(content)
	if err != nil || hex.EncodeToString(sum[:]) != r.Header.Get(agentwire.HeaderParcelSHA256) || int64(len(content)) != r.ContentLength {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	p.mu.Lock()
	if p.uploaded == nil {
		p.uploaded = map[string][]byte{}
	}
	p.uploaded[r.PathValue("name")] = content
	p.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

func (p *parcelServer) received() (map[string][]byte, []putRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.uploaded), slices.Clone(p.puts)
}

func (p *parcelServer) client(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(p.handler())
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "", "v0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func listed(files map[string]string) []agentwire.ParcelFile {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]agentwire.ParcelFile, 0, len(names))
	for _, name := range names {
		sum := sha256.Sum256([]byte(files[name]))
		out = append(out, agentwire.ParcelFile{Name: name, Size: int64(len(files[name])), SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// backupDir rend un dossier de destination hors du dossier d'essai, d'un
// nom que le motif d'un chemin de droit accepte.
func backupDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "backups")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// sourceFor branche receiveBackup sur un faux serveur, sans passer par le juge.
func sourceFor(t *testing.T, server *parcelServer) *fetcher {
	t.Helper()
	source := &fetcher{}
	source.use(server.client(t))
	source.setSession(parcelSession)
	return source
}

func TestBackupReceive_WritesVerifiesAndReportsEachFile(t *testing.T) {
	dir := filepath.Join(backupDir(t), "opencloud")
	files := map[string]string{archiveName: ageLike("sauvegarde chiffrée de test"), keyName: ageLike("clé scellée de test")}
	server := &parcelServer{files: files}

	output, err := receiveBackup(context.Background(), sourceFor(t, server), filepath.Dir(dir), agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: listed(files)})
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{}
	for _, file := range listed(files) {
		wantLines = append(wantLines, "written "+file.Name+" "+itoa(file.Size)+" sha256:"+file.SHA256)
	}
	var gotLines []string
	for _, line := range output {
		gotLines = append(gotLines, line.Line)
	}
	if !slices.Equal(gotLines, wantLines) {
		t.Fatalf("output %v, want %v", gotLines, wantLines)
	}
	// Le dossier a été créé, fermé aux autres comptes ; les fichiers aussi.
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %v %v", entries, err)
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		content, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if info.Mode().Perm() != 0o600 || string(content) != files[entry.Name()] || strings.HasSuffix(entry.Name(), partSuffix) {
			t.Errorf("%s: %v %q", entry.Name(), info.Mode(), content)
		}
	}
}

// Ce que le disque garde doit être ce que l'ordre a signé : une somme qui
// diffère, une taille de trop ou de moins, et rien n'est gardé.
func TestBackupReceive_DigestMismatchLeavesNoFile(t *testing.T) {
	for name, change := range map[string]func(file *agentwire.ParcelFile){
		"another sum":  func(file *agentwire.ParcelFile) { file.SHA256 = strings.Repeat("0", 64) },
		"size too big": func(file *agentwire.ParcelFile) { file.Size += 5 },
		"size too low": func(file *agentwire.ParcelFile) { file.Size -= 5 },
	} {
		t.Run(name, func(t *testing.T) {
			dir := backupDir(t)
			files := map[string]string{archiveName: ageLike("sauvegarde chiffrée de test")}
			files2 := listed(files)
			change(&files2[0])
			_, err := receiveBackup(context.Background(), sourceFor(t, &parcelServer{files: files}), dir, agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: files2})
			assertReason(t, err, agentwire.ReasonDigestMismatch)
			assertDirEmpty(t, dir)
		})
	}
}

func TestBackupReceive_RefusesANonAgeFile(t *testing.T) {
	dir := backupDir(t)
	files := map[string]string{archiveName: "#!/bin/sh\necho pas du age\n"}
	_, err := receiveBackup(context.Background(), sourceFor(t, &parcelServer{files: files}), dir, agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: listed(files)})
	assertReason(t, err, agentwire.ReasonNotAgeFile)
	assertDirEmpty(t, dir)
}

func TestBackupReceive_ReportsADownloadThatFails(t *testing.T) {
	dir := backupDir(t)
	files := map[string]string{archiveName: ageLike("x")}
	server := &parcelServer{files: map[string]string{}}
	_, err := receiveBackup(context.Background(), sourceFor(t, server), dir, agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: listed(files)})
	assertReason(t, err, agentwire.ReasonDownloadFailed)
	assertDirEmpty(t, dir)

	// Hors connexion, aucun jeton : rien n'est demandé au serveur.
	offline := &fetcher{}
	offline.use(server.client(t))
	before := len(server.requests)
	_, err = receiveBackup(context.Background(), offline, dir, agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: listed(files)})
	assertReason(t, err, agentwire.ReasonDownloadFailed)
	if len(server.requests) != before {
		t.Fatalf("a request left without a session: %v", server.requests)
	}
}

// Un dossier que l'unité de l'agent ne laisse pas écrire (0500) est dit
// path_unwritable. Root passe outre les droits d'un dossier : le test ne dit
// rien de lui.
func TestBackupReceive_ReportsAnUnwritablePath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := backupDir(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	files := map[string]string{archiveName: ageLike("x")}
	_, err := receiveBackup(context.Background(), sourceFor(t, &parcelServer{files: files}), dir, agentwire.BackupReceiveParams{Parcel: "colis1", Path: dir, Files: listed(files)})
	assertReason(t, err, agentwire.ReasonPathUnwritable)
}

// L'élagage garde les plus récentes par le nom, et ne touche à rien d'autre :
// ni une clé déposée, ni un .part, ni un fichier de l'opérateur, ni un
// dossier de la bonne forme.
func TestBackupPrune_KeepsTheNewestAndTouchesNothingElse(t *testing.T) {
	dir := backupDir(t)
	backups := []string{
		"opencloud-20261018-030000.tar.age", "opencloud-20261019-030000.tar.age", "opencloud-20261020-030000.tar.age",
		"opencloud-20261021-030000.tar.age",
	}
	others := []string{keyName, ".opencloud-20261001-030000.tar.age.part", "notes.txt", "opencloud-20261001-030000.tar", "opencloud-2026-030000.tar.age"}
	for _, name := range append(slices.Clone(backups), others...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "opencloud-20260101-030000.tar.age"), 0o700); err != nil {
		t.Fatal(err)
	}

	output, err := pruneBackups(dir, agentwire.BackupPruneParams{Path: dir, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range output {
		lines = append(lines, line.Line)
	}
	want := []string{"removed opencloud-20261018-030000.tar.age", "removed opencloud-20261019-030000.tar.age", "kept 2"}
	if !slices.Equal(lines, want) {
		t.Fatalf("output %v, want %v", lines, want)
	}
	left, _ := fs.ReadDir(os.DirFS(dir), ".")
	var names []string
	for _, entry := range left {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	wantLeft := append([]string{backups[2], backups[3], "opencloud-20260101-030000.tar.age"}, others...)
	slices.Sort(wantLeft)
	if !slices.Equal(names, wantLeft) {
		t.Fatalf("left %v, want %v", names, wantLeft)
	}

	// Rien à élaguer, ou un dossier qui n'existe pas encore : réussi.
	if output, err := pruneBackups(dir, agentwire.BackupPruneParams{Path: dir, Keep: 30}); err != nil || output[len(output)-1].Line != "kept 2" {
		t.Fatalf("nothing to prune: %v %v", output, err)
	}
	if output, err := pruneBackups(dir, agentwire.BackupPruneParams{Path: filepath.Join(dir, "absent"), Keep: 3}); err != nil || output[0].Line != "kept 0" {
		t.Fatalf("a folder that does not exist: %v %v", output, err)
	}
}

// Beaucoup à effacer : la sortie reste dans ce qu'un rapport accepte, sans
// quoi le serveur refuserait le rapport entier.
func TestBackupPrune_KeepsItsOutputWithinWhatAReportAccepts(t *testing.T) {
	dir := backupDir(t)
	for day := range 120 {
		name := "opencloud-2026" + twoDigits(1+day/28) + twoDigits(1+day%28) + "-030000.tar.age"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := pruneBackups(dir, agentwire.BackupPruneParams{Path: dir, Keep: 1})
	if err != nil || len(output) > agentwire.MaxOutputLines {
		t.Fatalf("%d lines, %v", len(output), err)
	}
	last := output[len(output)-1].Line
	if last != "kept 1" || !strings.HasPrefix(output[len(output)-2].Line, "removed ") {
		t.Fatalf("tail %q", last)
	}
	if left, _ := os.ReadDir(dir); len(left) != 1 {
		t.Fatalf("%d files left", len(left))
	}
}

func twoDigits(n int) string {
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

func itoa(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}

func assertReason(t *testing.T, err error, reason string) {
	t.Helper()
	if result := resultOf("run", nil, err); result.State != agentwire.ActionFailed || result.Reason != reason {
		t.Fatalf("result %+v (%v), want failed %s", result, err, reason)
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the folder holds %v (%v)", names, err)
	}
}

// longBench est un juge d'ordres dont le worker tourne et dont les colis
// viennent d'un faux serveur ; l'agent a le droit de recevoir sous dir.
type longBench struct {
	*orderBench
	dir    string
	server *parcelServer
}

func newLongBench(t *testing.T, files map[string]string) *longBench {
	t.Helper()
	dir := backupDir(t)
	b := newOrderBenchWithRights(t, []string{"observe", "backup.receive=" + dir})
	server := &parcelServer{files: files}
	b.runner.fetch.use(server.client(t))
	b.runner.fetch.setSession(parcelSession)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.runner.worker.run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &longBench{orderBench: b, dir: dir, server: server}
}

// signed encode et signe un ordre comme le serveur.
func (b *orderBench) signed(t *testing.T, body agentwire.OrderBody) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(agentwire.SignedOrder{Body: encoded, Signature: ed25519.Sign(b.private, agentwire.OrderSigningBytes(encoded))})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// until reçoit les résultats rapportés jusqu'à celui d'un état fini pour ce
// run ; il se règle sur le réveil de la boucle du flux, pas sur une pause.
func (b *orderBench) until(t *testing.T, runID string) []agentwire.ActionResult {
	t.Helper()
	var seen []agentwire.ActionResult
	timeout := time.After(10 * time.Second)
	for {
		if report := b.results.take(); report != nil {
			for _, result := range report.Results {
				if result.RunID != runID {
					continue
				}
				seen = append(seen, result)
				if result.State != agentwire.ActionAccepted {
					return seen
				}
			}
		}
		select {
		case <-b.results.ready:
		case <-timeout:
			t.Fatalf("no final result for %s, seen %+v", runID, seen)
		}
	}
}

func longOrder(seq int64, kind agentwire.Kind, params any) agentwire.OrderBody {
	body := order(seq)
	body.Kind = kind
	body.Params, _ = json.Marshal(params)
	return body
}

// Un ordre long est accepté tout de suite, puis fait par le worker : la
// réponse finale suit « accepté », jamais l'inverse.
func TestBackupReceive_IsAcceptedThenDoneByTheWorker(t *testing.T) {
	files := map[string]string{archiveName: ageLike("sauvegarde chiffrée de test")}
	b := newLongBench(t, files)
	body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: b.dir, Files: listed(files)})
	b.runner.receive(b.signed(t, body))
	results := b.until(t, body.RunID)
	if len(results) != 2 || results[0].State != agentwire.ActionAccepted || results[1].State != agentwire.ActionSucceeded {
		t.Fatalf("results %+v", results)
	}
	if !strings.HasPrefix(results[1].Output[0].Line, "written "+archiveName) {
		t.Fatalf("output %+v", results[1].Output)
	}
	if content, err := os.ReadFile(filepath.Join(b.dir, archiveName)); err != nil || string(content) != files[archiveName] {
		t.Fatalf("file %q %v", content, err)
	}
	if saved, _ := LoadIdentity(b.stateDir); saved.LastOrderSeq != 1 {
		t.Fatalf("seq %d", saved.LastOrderSeq)
	}
}

// Le juge applique le chemin du droit : un dossier hors de lui est refusé
// tout de suite, sans être accepté, et rien n'est demandé au serveur.
func TestBackupReceive_RefusesAPathOutsideItsRight(t *testing.T) {
	files := map[string]string{archiveName: ageLike("x")}
	b := newLongBench(t, files)
	for name, path := range map[string]string{"a neighbour": b.dir + "2", "above": filepath.Dir(b.dir), "dots": b.dir + "/../etc"} {
		body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: path, Files: listed(files)})
		b.runner.receive(b.signed(t, body))
		report := b.results.take()
		if report == nil || len(report.Results) != 1 || report.Results[0].State != agentwire.ActionRefused || report.Results[0].Reason != agentwire.ReasonPathOutsideRight {
			t.Fatalf("%s: %+v", name, report)
		}
		b.runner.identity.LastOrderSeq = 0
	}
	prune := longOrder(1, agentwire.KindBackupPrune, agentwire.BackupPruneParams{Path: b.dir + "2", Keep: 3})
	b.runner.receive(b.signed(t, prune))
	if report := b.results.take(); report == nil || report.Results[0].Reason != agentwire.ReasonPathOutsideRight {
		t.Fatalf("prune: %+v", report)
	}
	if len(b.server.requests) != 0 {
		t.Fatalf("the server was asked: %v", b.server.requests)
	}
}

// Un lien symbolique posé sous le chemin du droit ne fait pas sortir
// l'écriture ni l'élagage : le chemin qui le traverse est refusé, et rien
// n'est écrit ni effacé dans le dossier vers lequel il pointe.
func TestBackupReceive_RefusesASymlinkLeavingTheRight(t *testing.T) {
	granted := backupDir(t)
	outside := backupDir(t)
	if err := os.Symlink(outside, filepath.Join(granted, "sortie")); err != nil {
		t.Fatal(err)
	}
	old := "opencloud-20200101-030000.tar.age"
	for _, name := range []string{old, "opencloud-20200102-030000.tar.age"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{archiveName: ageLike("sauvegarde chiffrée de test")}
	server := &parcelServer{files: files}
	for _, destination := range []string{filepath.Join(granted, "sortie"), filepath.Join(granted, "sortie", "nuit")} {
		_, err := receiveBackup(context.Background(), sourceFor(t, server), granted, agentwire.BackupReceiveParams{Parcel: "colis1", Path: destination, Files: listed(files)})
		assertReason(t, err, agentwire.ReasonPathOutsideRight)
		_, err = pruneBackups(granted, agentwire.BackupPruneParams{Path: destination, Keep: 1})
		assertReason(t, err, agentwire.ReasonPathOutsideRight)
	}
	if len(server.requests) != 0 {
		t.Fatalf("the server was asked: %v", server.requests)
	}
	left, err := os.ReadDir(outside)
	if err != nil || len(left) != 2 || left[0].Name() != old {
		t.Fatalf("the folder behind the link changed: %v %v", left, err)
	}

	// Un .part laissé en lien vers un fichier du dossier n'est pas suivi :
	// il est remplacé, la cible reste intacte.
	target := filepath.Join(granted, "notes.txt")
	if err := os.WriteFile(target, []byte("à garder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(granted, partPrefix+archiveName+partSuffix)); err != nil {
		t.Fatal(err)
	}
	if _, err := receiveBackup(context.Background(), sourceFor(t, server), granted, agentwire.BackupReceiveParams{Parcel: "colis1", Path: granted, Files: listed(files)}); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(target); string(content) != "à garder" {
		t.Fatalf("the link was followed: %q", content)
	}
}

// Sans le droit backup.receive, l'ordre est refusé par le juge ; avec lui,
// des paramètres qui ne sont pas ceux d'une sauvegarde sont un ordre faux.
func TestBackupReceive_RefusesWhatIsNotABackup(t *testing.T) {
	b := newOrderBench(t)
	body := longOrder(1, agentwire.KindBackupReceive, agentwire.BackupReceiveParams{Parcel: "colis1", Path: "/srv/backups"})
	if result := b.send(t, body, b.private); result.Reason != "right_missing:backup.receive" {
		t.Fatalf("without the right: %+v", result)
	}
	files := map[string]string{"autre.txt": "x", "../opencloud-20261021-030000.tar.age": "x"}
	granted := newLongBench(t, files)
	for name, params := range map[string]agentwire.BackupReceiveParams{
		"no files":        {Parcel: "colis1", Path: granted.dir},
		"a free name":     {Parcel: "colis1", Path: granted.dir, Files: listed(map[string]string{"autre.txt": "x"})},
		"a path name":     {Parcel: "colis1", Path: granted.dir, Files: []agentwire.ParcelFile{{Name: "../" + archiveName, Size: 1, SHA256: strings.Repeat("a", 64)}}},
		"no parcel":       {Path: granted.dir, Files: listed(map[string]string{archiveName: "x"})},
		"a bad sum":       {Parcel: "colis1", Path: granted.dir, Files: []agentwire.ParcelFile{{Name: archiveName, Size: 1, SHA256: "xyz"}}},
		"a negative size": {Parcel: "colis1", Path: granted.dir, Files: []agentwire.ParcelFile{{Name: archiveName, Size: -1, SHA256: strings.Repeat("a", 64)}}},
	} {
		granted.runner.receive(granted.signed(t, longOrder(1, agentwire.KindBackupReceive, params)))
		report := granted.results.take()
		if report == nil || report.Results[0].State != agentwire.ActionFailed || report.Results[0].Reason != agentwire.ReasonBadOrder {
			t.Fatalf("%s: %+v", name, report)
		}
		granted.runner.identity.LastOrderSeq = 0
	}
}

// Le worker ne fait qu'un ordre long à la fois, dans l'ordre d'arrivée ; le
// juge, lui, ne les attend pas. Piloté par canaux.
func TestOrderWorker_RunsLongOrdersOneAtATime(t *testing.T) {
	worker := newOrderWorker()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	started := make(chan string, 4)
	release := make(chan struct{})
	finished := make(chan string, 4)
	job := func(name string) func(context.Context) {
		return func(context.Context) {
			started <- name
			<-release
			finished <- name
		}
	}
	for _, name := range []string{"first", "second", "third"} {
		if !worker.submit(job(name)) {
			t.Fatalf("%s was not queued", name)
		}
	}
	if got := <-started; got != "first" {
		t.Fatalf("started %s", got)
	}
	select {
	case got := <-started:
		t.Fatalf("%s started while the first still runs", got)
	default:
	}
	var order []string
	for range 3 {
		release <- struct{}{}
		order = append(order, <-finished)
	}
	if strings.Join(order, ",") != "first,second,third" {
		t.Fatalf("order %v", order)
	}
	if got := <-started; got != "second" {
		t.Fatalf("started %s", got)
	}
}

func TestOrderWorker_RefusesWhenItsQueueIsFull(t *testing.T) {
	worker := newOrderWorker()
	for range workerQueue {
		if !worker.submit(func(context.Context) {}) {
			t.Fatal("the queue refused before it was full")
		}
	}
	if worker.submit(func(context.Context) {}) {
		t.Fatal("a full queue accepted one more")
	}
}

// Le secret signé avec un ordre passe le juge, intact, et ne s'écrit nulle
// part : ni dans le journal de l'agent, ni dans l'identité sur disque, ni
// dans le résultat rapporté.
func TestJudge_KeepsTheSecretAndNeverLogsIt(t *testing.T) {
	var logs bytes.Buffer
	b := newOrderBench(t)
	b.runner.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	const value = "PHRASE=une valeur de test qui ne doit jamais sortir" // gitleaks:allow
	body := order(1)
	body.Secret, _ = json.Marshal(map[string]string{"env": value})

	judged, reason := b.runner.judge(b.signedOrder(t, body))
	if reason != "" || string(judged.Secret) != string(body.Secret) {
		t.Fatalf("judged %+v %q", judged, reason)
	}
	b.runner.receive(b.signed(t, order(2)))
	b.results.take()
	withSecret := order(3)
	withSecret.Secret = body.Secret
	b.runner.receive(b.signed(t, withSecret))
	report := b.results.take()
	reported, _ := json.Marshal(report)
	if report == nil || report.Results[0].State != agentwire.ActionSucceeded || strings.Contains(string(reported), "PHRASE") {
		t.Fatalf("report %s", reported)
	}
	if strings.Contains(logs.String(), "PHRASE") || strings.Contains(logs.String(), "valeur de test") {
		t.Fatalf("the secret reached the log: %s", logs.String())
	}
	identity, _ := b.stateDir.ReadFile(identityFile)
	if strings.Contains(string(identity), "PHRASE") {
		t.Fatal("the secret reached the identity file")
	}
}

func (b *orderBench) signedOrder(t *testing.T, body agentwire.OrderBody) agentwire.SignedOrder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return agentwire.SignedOrder{Body: encoded, Signature: ed25519.Sign(b.private, agentwire.OrderSigningBytes(encoded))}
}
