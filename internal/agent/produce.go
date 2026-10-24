package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/wait"
)

const (
	// Toute la production se fait en moins que MaxRunDuration (une heure, côté
	// serveur) : un producteur ou un envoi qui cale ne bloque pas les ordres
	// suivants au-delà.
	produceTimeout = 55 * time.Minute
	// L'archive brute puis l'archive chiffrée tiennent un instant ensemble sur
	// le disque de l'état : en dessous de ce libre, rien ne se lance.
	minProduceFreeBytes = 1 << 30
	rawSuffix           = ".raw"
	// Trois essais d'envoi sur une coupure du réseau, séparés de cette pause.
	uploadAttempts   = 3
	uploadRetryDelay = 10 * time.Second
	// Un résultat porte au plus MaxOutputLines lignes : la fin de la sortie
	// d'erreur du producteur, et celles que l'agent ajoute.
	producerTailLines = 32
	// La sortie d'erreur de sudo a le temps de sortir après le signal d'arrêt.
	producerStopDelay = 15 * time.Second
	maxParcelIDBytes  = 64
)

// L'environnement d'un producteur, remplacé et non hérité (conventions, « L'exécution
// de commandes »).
var producerEnvironment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

// producer sait lancer un producteur de sauvegarde : il écrit le flux brut de
// la cible dans le fichier qu'on lui donne, et dit comment nommer l'entrée du
// tar qui l'enveloppe. Les producteurs sont une table fermée de l'agent
// (newProduceTools) ; le droit backup.produce dit lesquels la machine permet.
type producer struct {
	// validTarget : la cible est de la forme que ce producteur lance. Elle
	// vient de l'ordre : rien n'est lancé sur une cible qui n'a pas cette forme.
	validTarget func(target string) bool
	run         func(ctx context.Context, request producerRequest) (producerResult, error)
}

type producerRequest struct {
	target string
	// raw reçoit le flux du producteur : c'est un fichier, jamais la mémoire.
	raw *os.File
	now time.Time
}

type producerResult struct {
	// Le nom de l'unique entrée du tar d'une archive.
	entry string
	// La fin de la sortie d'erreur du producteur, rendue même quand il échoue.
	log []agentwire.OutputLine
}

// produceTools est ce que l'ordre backup.produce touche : le dossier produce/
// de l'état de l'agent, la table des producteurs, le disque.
type produceTools struct {
	stateDir  *os.Root
	producers map[string]producer
	// freeBytes rend l'espace libre sous un chemin ; un test le remplace.
	freeBytes func(path string) (uint64, error)
	// retryDelay sépare deux envois ; un test le met à zéro.
	retryDelay time.Duration
}

func newProduceTools(stateDir *os.Root) produceTools {
	return produceTools{
		stateDir:   stateDir,
		producers:  map[string]producer{machine.ProducerProxmoxVzdump: newVzdumpProducer()},
		freeBytes:  freeBytesUnder,
		retryDelay: uploadRetryDelay,
	}
}

// produceJob est un ordre jugé : tout ce que l'exécution lira de lui.
type produceJob struct {
	params    agentwire.BackupProduceParams
	recipient age.Recipient
	producer  producer
}

// readBackupProduce juge l'ordre avant de l'accepter : le droit nomme ce
// producteur, l'agent le connaît, et le reste est de la forme attendue. Un
// ordre faux est refusé tout de suite, rien n'est lancé.
func readBackupProduce(body agentwire.OrderBody, capabilities []machine.Capability, producers map[string]producer) (produceJob, *agentwire.ActionResult) {
	bad := &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	var params agentwire.BackupProduceParams
	if err := json.Unmarshal(body.Params, &params); err != nil {
		return produceJob{}, bad
	}
	if !machine.CoversProducer(capabilities, params.Producer) {
		return produceJob{}, &agentwire.ActionResult{
			RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.RightMissing(machine.CapabilityBackupProduce),
		}
	}
	chosen, known := producers[params.Producer]
	if !known {
		return produceJob{}, &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonProducerUnknown}
	}
	recipient, err := backup.ParseRecipient(params.Recipient)
	if err != nil || !chosen.validTarget(params.Target) || !isProducePrefix(params.Prefix) ||
		params.Parcel == "" || len(params.Parcel) > maxParcelIDBytes {
		return produceJob{}, bad
	}
	return produceJob{params: params, recipient: recipient, producer: chosen}, nil
}

// isProducePrefix : un préfixe de sauvegarde qui n'est pas celui de la base.
// BackupFileName renommerait « database » en « opencloud », et une archive
// sous « opencloud » serait élaguée avec les sauvegardes de la base.
func isProducePrefix(prefix string) bool {
	return agentwire.IsBackupPrefix(prefix) && prefix != agentwire.DatabaseSubject && prefix != brand.ID
}

// produce fait l'ordre : le producteur écrit son flux brut dans produce/, il
// est emballé et chiffré pour la clé publique du trousseau, vérifié sur le
// disque, puis téléversé au serveur. Tout ce qui est écrit dans produce/ part
// à la fin, réussie ou non. L'agent n'a jamais la clé privée : il ne relit pas
// ce qu'il a chiffré. La production échoue avant d'écrire (untouched) si la
// machine ne laisse pas lancer le producteur ou n'a pas la place.
func (t produceTools) produce(ctx context.Context, checks hostChecks, sink parcelSink, job produceJob, now time.Time) ([]agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, produceTimeout)
	defer cancel()
	if reason := producerReason(ctx, checks, job.params.Producer); reason != "" {
		return nil, refuseWith(reason, errors.New("the producer cannot run on this machine"))
	}
	if free, err := t.freeBytes(t.stateDir.Name()); err == nil && free < minProduceFreeBytes {
		return nil, refuseWith(agentwire.ReasonDiskFull, fmt.Errorf("%d bytes free under the agent state", free))
	}
	dir, err := openProduceDir(t.stateDir)
	if err != nil {
		return nil, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	defer dir.Close()
	defer clearDirectory(dir)

	name := agentwire.BackupFileName(job.params.Prefix, now)
	entry, output, err := t.runProducer(ctx, dir, name, job, now)
	if err != nil {
		return output, err
	}
	file, err := packArchive(dir, name, entry, job.recipient)
	if err != nil {
		return output, err
	}
	output = append(output, stdout(fmt.Sprintf("written %s %d sha256:%s", file.Name, file.Size, file.SHA256)))
	if err := t.upload(ctx, sink, dir, job.params.Parcel, file); err != nil {
		output = append(output, agentwire.OutputLine{Stream: "stderr", Line: cleanLine("upload: " + err.Error())})
		return output, failWith(agentwire.ReasonUploadFailed, err)
	}
	return append(output, stdout("uploaded "+file.Name)), nil
}

// runProducer lance le producteur dans un fichier brut de produce/ et rend le
// nom de l'entrée du tar, avec la fin de la sortie d'erreur à rapporter. Le
// fichier brut part dès l'archive emballée (packArchive).
func (t produceTools) runProducer(ctx context.Context, dir *os.Root, name string, job produceJob, now time.Time) (packEntry, []agentwire.OutputLine, error) {
	raw, err := createProduceFile(dir, name+rawSuffix, os.O_RDWR)
	if err != nil {
		return packEntry{}, nil, err
	}
	result, runErr := job.producer.run(ctx, producerRequest{target: job.params.Target, raw: raw, now: now})
	if runErr != nil {
		_ = raw.Close()
		return packEntry{}, result.log, failWith(agentwire.ReasonProducerFailed, runErr)
	}
	info, err := raw.Stat()
	if err != nil {
		_ = raw.Close()
		return packEntry{}, result.log, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if info.Size() == 0 {
		_ = raw.Close()
		return packEntry{}, result.log, failWith(agentwire.ReasonProducerFailed, errors.New("the producer wrote nothing"))
	}
	// Le producteur et l'agent partagent la position du fichier : la lecture
	// repart du début.
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		_ = raw.Close()
		return packEntry{}, result.log, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return packEntry{name: result.entry, content: raw, size: info.Size()}, result.log, nil
}

// packEntry est le flux brut à emballer : le nom de son entrée de tar, le
// fichier qui le porte (fermé par packArchive) et sa taille.
type packEntry struct {
	name    string
	content *os.File
	size    int64
}

// packArchive emballe le flux brut dans un tar d'une entrée, chiffré age pour
// la clé publique, écrit dans un .part, puis renommé et relu depuis le disque
// comme le fait la réception : taille, somme et en-tête sont ceux du disque.
// Le brut est effacé dès qu'il est emballé, pour ne pas tenir deux archives.
func packArchive(dir *os.Root, name string, entry packEntry, recipient age.Recipient) (agentwire.ParcelFile, error) {
	part := name + partSuffix
	target, err := createProduceFile(dir, part, os.O_WRONLY)
	if err != nil {
		_ = entry.content.Close()
		return agentwire.ParcelFile{}, err
	}
	hasher := sha256.New()
	packErr := backup.WriteEntryArchive(io.MultiWriter(target, hasher), recipient, entry.name, entry.content, entry.size)
	_ = entry.content.Close()
	_ = dir.Remove(name + rawSuffix)
	if packErr != nil {
		_ = target.Close()
		return agentwire.ParcelFile{}, writeFailure(packErr)
	}
	syncErr := target.Sync()
	info, statErr := target.Stat()
	if err := errors.Join(syncErr, statErr, target.Close()); err != nil {
		return agentwire.ParcelFile{}, writeFailure(err)
	}
	file := agentwire.ParcelFile{Name: name, Size: info.Size(), SHA256: hex.EncodeToString(hasher.Sum(nil))}
	if err := dir.Rename(part, name); err != nil {
		return agentwire.ParcelFile{}, writeFailure(err)
	}
	// fsync du dossier, sinon le rename peut se perdre à la coupure.
	if err := fsx.SyncDir(dir, "."); err != nil {
		return agentwire.ParcelFile{}, writeFailure(err)
	}
	if err := verifyOnDisk(dir, file); err != nil {
		return agentwire.ParcelFile{}, err
	}
	return file, nil
}

// upload envoie l'archive au serveur, trois fois au plus : une coupure du
// réseau ou une session tombée se relance, un refus du serveur non (il dirait
// la même chose).
func (t produceTools) upload(ctx context.Context, sink parcelSink, dir *os.Root, parcel string, file agentwire.ParcelFile) error {
	upload := parcelUpload{
		name: file.Name, size: file.Size, sum: file.SHA256,
		open: func() (io.ReadCloser, error) { return dir.Open(file.Name) },
	}
	var err error
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		err = sink.put(ctx, parcel, upload)
		var refusal *ServerError
		if err == nil || errors.As(err, &refusal) {
			return err
		}
		if attempt < uploadAttempts && !wait.For(ctx, t.retryDelay) {
			return ctx.Err()
		}
	}
	return err
}

// writeFailure : une écriture de produce/ qui manque. Un disque plein se dit
// disk_full, le reste path_unwritable.
func writeFailure(err error) error {
	if errors.Is(err, syscall.ENOSPC) {
		return failWith(agentwire.ReasonDiskFull, err)
	}
	return failWith(agentwire.ReasonPathUnwritable, err)
}

// openProduceDir ouvre produce/ sous l'état de l'agent, et le crée (0700) s'il
// manque : la préparation le pose, mais une machine préparée avant ne l'a pas.
func openProduceDir(stateDir *os.Root) (*os.Root, error) {
	if err := stateDir.Mkdir(brand.AgentProduceDir, backupDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	if err := requireProduceDir(stateDir); err != nil {
		return nil, err
	}
	return stateDir.OpenRoot(brand.AgentProduceDir)
}

// requireProduceDir : produce/ est un vrai dossier. os.Root suit un lien qui
// reste sous l'état : un produce/ qui pointerait vers l'état lui-même serait
// vidé avec la clé de l'agent.
func requireProduceDir(stateDir *os.Root) error {
	info, err := stateDir.Lstat(brand.AgentProduceDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", brand.AgentProduceDir)
	}
	return nil
}

// createProduceFile crée un fichier neuf (0600) de produce/. Un reste du même
// nom est retiré d'abord : le fichier ne s'écrit jamais à travers ce qui
// porterait déjà son nom.
func createProduceFile(dir *os.Root, name string, access int) (*os.File, error) {
	if err := dir.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	file, err := dir.OpenFile(name, access|os.O_CREATE|os.O_EXCL, backupFileMode)
	if err != nil {
		return nil, writeFailure(err)
	}
	return file, nil
}

// clearDirectory efface tout produce/ : rien d'une archive n'y reste après un
// ordre, réussi ou non.
func clearDirectory(dir *os.Root) {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = dir.RemoveAll(entry.Name())
	}
}

// clearProduceDir vide produce/ au démarrage de l'agent : un arrêt en pleine
// production y a laissé un .raw ou un .part de la taille d'une machine
// virtuelle. Un dossier qui n'existe pas n'a rien à vider.
func clearProduceDir(stateDir *os.Root) error {
	err := requireProduceDir(stateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, err := stateDir.OpenRoot(brand.AgentProduceDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	clearDirectory(dir)
	return nil
}

// Le producteur proxmox-vzdump : sudo lance le script de root que la
// préparation pose (brand.VzdumpWrapperPath), qui lance vzdump hors du bac à
// sable de l'unité. vzdump écrit l'archive sur sa sortie standard (--stdout) et
// son journal sur la sortie d'erreur.
var (
	vzdumpTarget = regexp.MustCompile(`^\d{1,9}$`)
	// La ligne où vzdump nomme le type de l'invité qu'il sauvegarde.
	vzdumpGuestLine = regexp.MustCompile(`Starting Backup of VM \d+ \((qemu|lxc)\)`)
)

const (
	guestQEMU = "qemu"
	guestLXC  = "lxc"
	// La date du nom que vzdump donne à son archive.
	vzdumpTimeLayout = "2006_01_02-15_04_05"
	maxGuestLine     = 4 << 10
)

// vzdumpProducer : sudo est le programme du PATH quand il est vide ; wrapper
// est le script de root. Un test donne les siens.
type vzdumpProducer struct {
	sudo    string
	wrapper string
}

func newVzdumpProducer() producer {
	runner := vzdumpProducer{wrapper: brand.VzdumpWrapperPath}
	return producer{validTarget: vzdumpTarget.MatchString, run: runner.run}
}

func (v vzdumpProducer) run(ctx context.Context, request producerRequest) (producerResult, error) {
	sudo := v.sudo
	if sudo == "" {
		found, err := exec.LookPath("sudo")
		if err != nil {
			return producerResult{}, err
		}
		sudo = found
	}
	// bounded: sudo est celui du PATH de l'unité, le script une constante de
	// brand, la cible jugée par vzdumpTarget (chiffres seuls : jamais une option),
	// les autres arguments des constantes.
	command := exec.CommandContext(ctx, sudo, "-n", v.wrapper, request.target, // #nosec G204 -- bounded: voir ci-dessus.
		"--mode", "snapshot", "--compress", "zstd", "--stdout", "--quiet", "1")
	command.Env = producerEnvironment
	command.Stdout = request.raw
	tail := newLineTail(producerTailLines)
	guest := &guestKindWriter{}
	command.Stderr = io.MultiWriter(tail.writer("stderr"), guest)
	// Un arrêt demande poliment : sudo relaie le signal à ce qu'il lance.
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = producerStopDelay
	err := command.Run()
	tail.flush()
	result := producerResult{log: tail.lines()}
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("vzdump: %w", ctx.Err())
		}
		return result, err
	}
	result.entry = vzdumpEntryName(guest.kind(), request.target, request.now)
	return result, nil
}

// vzdumpEntryName nomme l'entrée du tar comme vzdump nomme ses archives, pour
// que qmrestore ou pct restore la reconnaisse une fois extraite.
func vzdumpEntryName(guest, target string, at time.Time) string {
	extension := "vma.zst"
	if guest == guestLXC {
		extension = "tar.zst"
	}
	return "vzdump-" + guest + "-" + target + "-" + at.UTC().Format(vzdumpTimeLayout) + "." + extension
}

// guestKindWriter lit la sortie d'erreur de vzdump ligne à ligne pour y trouver
// le type de l'invité. À défaut de la ligne qui le nomme, une machine
// virtuelle (qemu), sauf si une ligne parle de lxc : le type ne fait que
// nommer l'entrée du tar, le contenu ne change pas.
type guestKindWriter struct {
	line   []byte
	named  string
	sawLXC bool
}

func (g *guestKindWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' || b == '\r' {
			g.scan()
			continue
		}
		if len(g.line) < maxGuestLine {
			g.line = append(g.line, b)
		}
	}
	return len(data), nil
}

func (g *guestKindWriter) scan() {
	line := string(g.line)
	g.line = g.line[:0]
	if match := vzdumpGuestLine.FindStringSubmatch(line); match != nil && g.named == "" {
		g.named = match[1]
	}
	if strings.Contains(strings.ToLower(line), guestLXC) {
		g.sawLXC = true
	}
}

func (g *guestKindWriter) kind() string {
	g.scan()
	switch {
	case g.named != "":
		return g.named
	case g.sawLXC:
		return guestLXC
	}
	return guestQEMU
}
