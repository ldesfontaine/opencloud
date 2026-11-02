package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Au-delà, ce n'est plus la configuration d'une infra qu'on lit.
const (
	maxBackupJobs     = 256
	maxJobGuestIDs    = 5000
	maxRetentionCount = 100000
)

// BackupJob est une tâche vzdump de la grappe, telle que /cluster/backup la
// décrit. Proxmox la mène ; openCloud la montre, ne la lance jamais.
type BackupJob struct {
	ID       string
	Schedule string
	// Zéro : Proxmox ne la dit pas (une tâche coupée, une version ancienne).
	NextRun time.Time
	Storage string
	Enabled bool
	// Node borne la tâche aux invités d'un nœud ; vide : toute la grappe.
	Node      string
	AllGuests bool
	VMIDs     []int
	Excluded  []int
	Retention Retention
	Comment   string
}

// Retention est ce que prune-backups garde. Known faux : la tâche n'en dit
// rien, le stockage décide (et le relevé ne le lit pas).
type Retention struct {
	Known   bool
	KeepAll bool
	Last    int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// Covers dit si la tâche sauvegarde cet invité : tous ceux de la grappe (ou
// de son nœud), moins les exclus, ou ceux de sa liste. Une tâche coupée ne
// couvre rien : elle ne tourne pas.
func (j BackupJob) Covers(guest Guest) bool {
	if !j.Enabled || guest.Template {
		return false
	}
	if j.Node != "" && j.Node != guest.Node {
		return false
	}
	if j.AllGuests {
		return !slices.Contains(j.Excluded, guest.VMID)
	}
	return slices.Contains(j.VMIDs, guest.VMID)
}

// JobFor rend la tâche qui sauvegarde l'invité le plus tôt : celle dont la
// prochaine exécution vient d'abord, une date connue avant une inconnue.
func JobFor(jobs []BackupJob, guest Guest) (BackupJob, bool) {
	var found BackupJob
	isFound := false
	for _, job := range jobs {
		if !job.Covers(guest) {
			continue
		}
		if !isFound || runsBefore(job, found) {
			found, isFound = job, true
		}
	}
	return found, isFound
}

func runsBefore(candidate, current BackupJob) bool {
	if current.NextRun.IsZero() {
		return !candidate.NextRun.IsZero()
	}
	return !candidate.NextRun.IsZero() && candidate.NextRun.Before(current.NextRun)
}

// jobEntry est une ligne de /cluster/backup. Proxmox rend ses booléens et ses
// nombres tantôt en nombre, tantôt en texte : flexibleInt lit les deux.
type jobEntry struct {
	ID           string          `json:"id"`
	Schedule     string          `json:"schedule"`
	Starttime    string          `json:"starttime"`
	NextRun      flexibleInt     `json:"next-run"`
	Storage      string          `json:"storage"`
	Enabled      *flexibleInt    `json:"enabled"`
	Node         string          `json:"node"`
	All          flexibleInt     `json:"all"`
	VMID         string          `json:"vmid"`
	Exclude      string          `json:"exclude"`
	PruneBackups json.RawMessage `json:"prune-backups"`
	Comment      string          `json:"comment"`
}

// BackupJobs lit les tâches de sauvegarde de la grappe. Le jeton sans
// Sys.Audit sur « / » reçoit 403 : unreadable vrai, sans erreur — la fiche
// dit ce qui manque, comme pour des archives illisibles. Toute autre réponse
// inattendue se lit de même : « inconnu », jamais « aucune tâche », et le
// relevé garde ses VM et ses stockages.
func (c *Client) BackupJobs(ctx context.Context) (jobs []BackupJob, unreadable bool, err error) {
	var listed []jobEntry
	err = c.get(ctx, "/cluster/backup", nil, &listed)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if errors.Is(err, errForbidden) || errors.Is(err, ErrBadResponse) {
		return []BackupJob{}, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	jobs = []BackupJob{}
	for _, entry := range listed {
		if len(jobs) == maxBackupJobs {
			break
		}
		if entry.ID == "" {
			continue
		}
		jobs = append(jobs, jobOf(entry))
	}
	return jobs, false, nil
}

func jobOf(entry jobEntry) BackupJob {
	schedule := entry.Schedule
	// Une tâche d'avant les horaires de calendrier ne dit que starttime.
	if schedule == "" {
		schedule = entry.Starttime
	}
	job := BackupJob{
		ID: boundedText(entry.ID), Schedule: boundedText(schedule), Storage: boundedText(entry.Storage),
		Enabled: entry.Enabled == nil || entry.Enabled.value != 0, Node: boundedText(entry.Node),
		AllGuests: entry.All.value == 1, VMIDs: guestIDList(entry.VMID), Excluded: guestIDList(entry.Exclude),
		Retention: retentionOf(entry.PruneBackups), Comment: boundedText(entry.Comment),
	}
	if entry.NextRun.value > 0 {
		job.NextRun = time.Unix(entry.NextRun.value, 0).UTC()
	}
	return job
}

// guestIDList lit « 101,105 » (ou séparé d'espaces) ; ce qui n'est pas un
// numéro est ignoré.
func guestIDList(text string) []int {
	ids := []int{}
	for _, item := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if len(ids) == maxJobGuestIDs {
			break
		}
		id, err := strconv.Atoi(item)
		if err == nil && id > 0 && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// retentionOf lit prune-backups, rendu en chaîne (« keep-daily=14,… ») ou en
// objet selon la version de Proxmox. Absent ou illisible : inconnue.
func retentionOf(raw json.RawMessage) Retention {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return Retention{}
	}
	values := map[string]string{}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		for item := range strings.SplitSeq(text, ",") {
			key, value, found := strings.Cut(strings.TrimSpace(item), "=")
			if found {
				values[key] = value
			}
		}
	} else {
		var object map[string]flexibleInt
		if json.Unmarshal(raw, &object) != nil {
			return Retention{}
		}
		for key, value := range object {
			values[key] = strconv.FormatInt(value.value, 10)
		}
	}
	return Retention{
		Known: len(values) > 0, KeepAll: keepCount(values, "keep-all") == 1,
		Last: keepCount(values, "keep-last"), Daily: keepCount(values, "keep-daily"), Weekly: keepCount(values, "keep-weekly"),
		Monthly: keepCount(values, "keep-monthly"), Yearly: keepCount(values, "keep-yearly"),
	}
}

func keepCount(values map[string]string, key string) int {
	count, err := strconv.Atoi(values[key])
	if err != nil || count < 0 || count > maxRetentionCount {
		return 0
	}
	return count
}

// flexibleInt lit un entier écrit en nombre ou en texte ; autre chose vaut 0.
type flexibleInt struct {
	value int64
}

func (f *flexibleInt) UnmarshalJSON(raw []byte) error {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		f.value, _ = number.Int64()
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		f.value, _ = strconv.ParseInt(text, 10, 64)
		return nil
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil && flag {
		f.value = 1
		return nil
	}
	f.value = 0
	return nil
}
