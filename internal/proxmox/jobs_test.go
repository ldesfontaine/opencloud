package proxmox

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"
)

// Une réponse de /cluster/backup telle qu'un Proxmox VE 8 la rend : une tâche
// de toute la grappe vers un partage NFS, avec sa rétention ; une tâche
// coupée, bornée à une liste ; une tâche d'avant les horaires (starttime),
// bornée à un nœud, sa rétention en objet.
const clusterBackupAnswer = `{"data":[
	{"id":"nuit-pi","type":"vzdump","schedule":"03:00","next-run":1793329200,"storage":"pi-sauvegardes","enabled":1,
	 "all":1,"exclude":"9000","mode":"snapshot","compress":"zstd","prune-backups":"keep-daily=14,keep-weekly=8,keep-monthly=3",
	 "comment":"chaque nuit vers le Pi","notes-template":"{{guestname}}"},
	{"id":"backup-5c1e2a7b-0d4e","type":"vzdump","schedule":"sun 01:00","storage":"local","enabled":0,"vmid":"200,5012",
	 "prune-backups":"keep-last=2"},
	{"id":"ancienne","type":"vzdump","starttime":"02:30","dow":"mon,tue","storage":"local","node":"pve2","all":"1",
	 "prune-backups":{"keep-yearly":"1","keep-monthly":2}}
]}`

func jobsAPI(t *testing.T, status int, answer string) *fakeAPI {
	t.Helper()
	api := newFakeAPI(t)
	api.handler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api2/json/cluster/backup" {
			return false
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
		return true
	}
	return api
}

// La tâche « nuit-pi » se lit telle que Proxmox la décrit : son stockage, sa
// prochaine exécution, sa rétention ; rien n'est en dur, tout vient de la
// réponse.
func TestBackupJobs_ReadsTheVzdumpJobsOfTheCluster(t *testing.T) {
	api := jobsAPI(t, http.StatusOK, clusterBackupAnswer)
	jobs, unreadable, err := NewClient(api.credentials(api.pin())).BackupJobs(context.Background())
	if err != nil || unreadable || len(jobs) != 3 {
		t.Fatalf("jobs %+v unreadable %v err %v", jobs, unreadable, err)
	}
	night := jobs[0]
	if night.ID != "nuit-pi" || night.Storage != "pi-sauvegardes" || night.Schedule != "03:00" || !night.Enabled || !night.AllGuests {
		t.Errorf("nuit-pi : %+v", night)
	}
	if !night.NextRun.Equal(time.Unix(1793329200, 0)) || !slices.Equal(night.Excluded, []int{9000}) || night.Comment != "chaque nuit vers le Pi" {
		t.Errorf("nuit-pi : %+v", night)
	}
	if want := (Retention{Known: true, Daily: 14, Weekly: 8, Monthly: 3}); night.Retention != want {
		t.Errorf("rétention %+v, attendue %+v", night.Retention, want)
	}
	listed := jobs[1]
	if listed.Enabled || listed.AllGuests || !slices.Equal(listed.VMIDs, []int{200, 5012}) || listed.Retention.Last != 2 || !listed.NextRun.IsZero() {
		t.Errorf("tâche coupée : %+v", listed)
	}
	old := jobs[2]
	if old.Schedule != "02:30" || old.Node != "pve2" || !old.AllGuests || old.Retention != (Retention{Known: true, Monthly: 2, Yearly: 1}) {
		t.Errorf("ancienne tâche : %+v", old)
	}
}

// Sans Sys.Audit, Proxmox répond 403 : les tâches sont « inconnues », le
// relevé continue avec ses VM et ses stockages.
func TestBackupJobs_AForbiddenAnswerIsUnknownNotEmpty(t *testing.T) {
	api := jobsAPI(t, http.StatusForbidden, `{"data":null}`)
	reading, err := NewClient(api.credentials(api.pin())).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reading.JobsUnreadable || len(reading.Jobs) != 0 || len(reading.Guests) == 0 {
		t.Fatalf("reading %+v", reading)
	}
}

// Le rapprochement : « all » couvre tous les invités (moins les exclus, et
// ceux d'un autre nœud si la tâche en nomme un), une liste ses seuls numéros ;
// une tâche coupée ne couvre rien. La tâche la plus proche gagne.
func TestJobFor_MatchesEachGuestToTheJobThatBacksItUp(t *testing.T) {
	jobs := []BackupJob{
		{ID: "nuit-pi", Enabled: true, AllGuests: true, Excluded: []int{9000}, NextRun: time.Unix(2000, 0), Storage: "pi-sauvegardes"},
		{ID: "coupee", Enabled: false, VMIDs: []int{101}, NextRun: time.Unix(1000, 0)},
		{ID: "liste", Enabled: true, VMIDs: []int{105}, NextRun: time.Unix(1500, 0)},
		{ID: "autre-noeud", Enabled: true, AllGuests: true, Node: "pve2", NextRun: time.Unix(500, 0)},
	}
	cases := map[int]string{101: "nuit-pi", 105: "liste", 9000: ""}
	for vmid, want := range cases {
		job, found := JobFor(jobs, Guest{VMID: vmid, Node: "pve"})
		if got := job.ID; found != (want != "") || got != want {
			t.Errorf("VM %d : %q (%v), attendue %q", vmid, got, found, want)
		}
	}
	if job, _ := JobFor(jobs, Guest{VMID: 101, Node: "pve2"}); job.ID != "autre-noeud" {
		t.Errorf("sur pve2, la tâche du nœud passe d'abord : %q", job.ID)
	}
	if _, found := JobFor(jobs, Guest{VMID: 101, Node: "pve", Template: true}); found {
		t.Error("un modèle n'est jamais sauvegardé")
	}
}
