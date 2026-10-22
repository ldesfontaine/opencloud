import { expect, test } from "vitest";

import type { Job, JobStatus, Run, RunOutcome } from "../api/jobs";
import type { Translate } from "../i18n/context";
import { awaitsFirstPing, currentStreak, exitCodeTone, isSignaled, jobFact, jobFormOf, jobRequestOf, jobsSubtitle, jobTones, lastSuccessText, outcomeKey, runHistoryLimit, runTones } from "./jobs";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

const now = new Date(2026, 9, 12, 9, 55, 0).getTime();

function at(hour: number, minute: number): string {
  return new Date(2026, 9, 12, hour, minute, 0).toISOString();
}

const minute = 60 * 1000;
const hour = 60 * minute;
const day = 24 * hour;

// Des exécutions finies à des instants précis : [issue, il y a combien de
// temps], de la plus récente à la plus ancienne. Un « missed » n'a ni début
// ni code, un ping nu non plus (withCode à faux).
function runsAgo(entries: Array<[RunOutcome, number]>, withCode = true): Run[] {
  return entries.map(([outcome, elapsed], index) => {
    const finishedAt = new Date(now - elapsed).toISOString();
    const hasCode = withCode && outcome !== "missed";
    return {
      id: entries.length - index,
      started_at: null,
      completed_at: finishedAt,
      duration_ms: null,
      exit_code: hasCode ? (outcome === "failure" ? 1 : 0) : null,
      outcome,
      payload: "",
    };
  });
}

const job: Job = {
  id: "a0dd0f6d04899b5d",
  name: "Renouvellement certbot",
  machine_id: "",
  machine_name: "",
  status: "new",
  interval_seconds: 120,
  grace_seconds: 60,
  last_ping_at: null,
  next_deadline_at: null,
  run_started_at: null,
  last_exit_code: null,
  last_duration_ms: null,
  created_at: at(9, 44),
};

// Les exécutions arrivent de la plus récente à la plus ancienne, une par minute.
function runs(...outcomes: RunOutcome[]): Run[] {
  return outcomes.map((outcome, index) => ({
    id: outcomes.length - index,
    started_at: outcome === "missed" ? null : at(9, 54 - index),
    completed_at: outcome === "in_progress" ? null : at(9, 54 - index),
    duration_ms: outcome === "missed" ? null : 2000,
    exit_code: outcome === "failure" ? 1 : outcome === "missed" ? null : 0,
    outcome,
    payload: "",
  }));
}

test("une tâche reste à installer tant qu'aucun ping n'est arrivé", () => {
  expect(awaitsFirstPing(job)).toBe(true);
});

test("une tâche est installée dès le premier ping, même en échec", () => {
  expect(awaitsFirstPing({ ...job, status: "failed", last_ping_at: at(9, 50), last_exit_code: 1 })).toBe(false);
});

test("currentStreak compte les exécutions de même issue depuis la plus récente", () => {
  expect(currentStreak(runs("failure", "failure", "timeout", "success", "failure"))).toEqual({
    succeeded: false,
    count: 3,
    isOpenEnded: false,
    since: at(9, 52),
    brokenBy: "success",
    beforeBreak: null,
  });
  expect(currentStreak(runs("success", "success", "failure"))?.count).toBe(2);
});

test("currentStreak ignore l'exécution en cours et ne dit rien sans exécution finie", () => {
  expect(currentStreak(runs("in_progress", "success", "failure"))).toMatchObject({ succeeded: true, count: 1 });
  expect(currentStreak(runs("in_progress"))).toBeNull();
  expect(currentStreak([])).toBeNull();
});

test("currentStreak dit qu'une série peut être plus longue quand elle couvre tout l'historique rendu", () => {
  const all = runs(...Array<RunOutcome>(runHistoryLimit).fill("success"));
  expect(currentStreak(all)).toMatchObject({ count: runHistoryLimit, isOpenEnded: true });
  expect(currentStreak(runs("success", "success"))).toMatchObject({ count: 2, isOpenEnded: false });
});

test("jobFact dit la série d'échecs et depuis quand", () => {
  const failed: Job = { ...job, status: "failed", last_ping_at: at(9, 54), last_exit_code: 1 };
  expect(jobFact(t, failed, runs("failure", "failure", "failure", "failure", "success"), now)).toBe("job.fact_failures:4:09:51");
  expect(jobFact(t, failed, runs("failure", "success"), now)).toBe("job.fact_failed");
  expect(jobFact(t, failed, [], now)).toBe("job.fact_failed");
  expect(jobFact(t, failed, runs(...Array<RunOutcome>(runHistoryLimit).fill("failure")), now)).toBe(`job.fact_failures_all:${runHistoryLimit}`);
});

test("jobFact dit depuis quand une tâche à l'heure va bien", () => {
  const onTime: Job = { ...job, status: "on_time", last_ping_at: at(9, 54) };
  // Trois réussites après un échec : depuis la première d'entre elles.
  expect(jobFact(t, onTime, runs("success", "success", "success", "failure"), now)).toBe("job.fact_on_time_since:time.minutes:3");
  expect(jobFact(t, onTime, runs("success", "failure"), now)).toBe("job.fact_on_time_since:time.minutes:1");
  // Aucune rupture dans l'historique rendu : « au moins ».
  expect(jobFact(t, onTime, runs(...Array<RunOutcome>(runHistoryLimit).fill("success")), now)).toBe(`job.fact_on_time_at_least:time.minutes:${runHistoryLimit}`);
  // Un historique court sans rupture : la tâche est jeune, pas « au moins ».
  expect(jobFact(t, onTime, runs("success", "success", "success"), now)).toBe("job.fact_on_time_since:time.minutes:3");
  expect(jobFact(t, onTime, runs("success"), now)).toBe("job.fact_success");
  expect(jobFact(t, onTime, [], now)).toBe("job.fact_on_time");
});

test("jobFact dit la reprise d'une tâche restée sans signal, et la durée du trou", () => {
  const onTime: Job = { ...job, status: "on_time", last_ping_at: new Date(now - 10 * minute).toISOString() };
  const resumed = runsAgo([
    ["success", 10 * minute],
    ["missed", 4 * hour],
    ["success", 5 * hour],
    ["success", 6 * hour],
    ["success", 7 * hour],
  ]);
  // Repartie il y a 10 min ; 4 h 50 entre la réussite d'avant et celle d'après, tronquées à 4 h.
  expect(jobFact(t, onTime, resumed, now)).toBe("job.fact_resumed:time.minutes:10:time.hours:4");
  // Sans réussite d'avant dans l'historique rendu : le trou n'a pas de durée.
  expect(jobFact(t, onTime, runsAgo([["success", 10 * minute], ["missed", 4 * hour]]), now)).toBe("job.fact_resumed_after_late:time.minutes:10");
  // Un échec avant le trou est un signal : le trou part de lui, pas de la réussite d'avant.
  expect(jobFact(t, onTime, runsAgo([["success", 10 * minute], ["missed", 2 * hour], ["failure", 3 * hour], ["success", 2 * day]]), now)).toBe(
    "job.fact_resumed:time.minutes:10:time.hours:2",
  );
  // Plusieurs réussites depuis la reprise : la série part de la première.
  expect(jobFact(t, onTime, runsAgo([["success", 10 * minute], ["success", 70 * minute], ["missed", 3 * hour], ["success", 4 * hour]]), now)).toBe(
    "job.fact_resumed:time.hours:1:time.hours:2",
  );
});

test("jobFact ne parle plus de reprise quand elle date de plus d'un jour", () => {
  const onTime: Job = { ...job, status: "on_time", last_ping_at: new Date(now - 10 * minute).toISOString() };
  const old = runsAgo([
    ["success", 10 * minute],
    ["success", 1 * day],
    ["success", 3 * day],
    ["missed", 3 * day + 2 * hour],
    ["success", 4 * day],
  ]);
  expect(jobFact(t, onTime, old, now)).toBe("job.fact_on_time_since:time.days:3");
});

test("currentStreak : un missed coupe une série de réussites, sans être un échec", () => {
  const streak = currentStreak(runs("success", "success", "success", "missed", "success"));
  expect(streak).toMatchObject({ succeeded: true, count: 3, brokenBy: "missed" });
  expect(streak?.beforeBreak?.id).toBe(1);
  expect(currentStreak(runs("success", "missed", "failure", "success"))?.beforeBreak?.outcome).toBe("failure");
  expect(currentStreak(runs("success", "failure"))).toMatchObject({ brokenBy: "failure" });
  expect(currentStreak(runs("success", "success"))).toMatchObject({ brokenBy: null, beforeBreak: null });
});

test("currentStreak : une série d'échecs ne compte ni ne traverse un missed", () => {
  expect(currentStreak(runs("failure", "failure", "missed", "failure"))).toMatchObject({ succeeded: false, count: 2, brokenBy: "missed" });
  expect(currentStreak(runs("failure", "timeout", "failure"))).toMatchObject({ succeeded: false, count: 3, brokenBy: null });
  // Un missed seul, plus récent que tout : aucune exécution, aucune série d'échecs.
  expect(currentStreak(runs("missed", "success"))).toMatchObject({ succeeded: false, count: 0, since: null });
});

test("jobFact : deux échecs après un missed disent « 2 échecs de suite », le missed n'est pas compté", () => {
  const failed: Job = { ...job, status: "failed", last_ping_at: at(9, 54), last_exit_code: 1 };
  expect(jobFact(t, failed, runs("failure", "failure", "missed", "success"), now)).toBe("job.fact_failures:2:09:53");
});

test("un ping nu est un passage signalé, pas une réussite constatée", () => {
  const bare = runsAgo([["success", 2 * minute]], false);
  expect(bare[0] && isSignaled(bare[0])).toBe(true);
  expect(bare[0] && outcomeKey(bare[0])).toBe("job.outcome_signaled");
  const withCode = runs("success");
  expect(withCode[0] && isSignaled(withCode[0])).toBe(false);
  expect(withCode[0] && outcomeKey(withCode[0])).toBe("job.outcome_success");
  expect(outcomeKey(runs("missed")[0] as Run)).toBe("job.outcome_missed");
  expect(runTones.missed).toBe("warn");
});

test("jobFact dit le retard, l'attente, la pause et l'exécution en cours", () => {
  expect(jobFact(t, job, [], now)).toBe("job.fact_new");
  expect(jobFact(t, { ...job, status: "late" }, [], now)).toBe("job.fact_never_pinged");
  expect(jobFact(t, { ...job, status: "late", last_ping_at: at(9, 47) }, runs("success"), now)).toBe("job.fact_late:time.minutes:8");
  expect(jobFact(t, { ...job, status: "paused" }, runs("success"), now)).toBe("job.fact_paused");
  expect(jobFact(t, { ...job, status: "started", run_started_at: at(9, 52) }, runs("in_progress"), now)).toBe("job.fact_started:time.minutes:3");
});

test("lastSuccessText dit toujours la dernière réussite, quelle que soit la tâche", () => {
  expect(lastSuccessText(t, runs("failure", "failure", "success"), now)).toBe("job.last_success:09:52:job.ago:time.minutes:3");
  expect(lastSuccessText(t, runs("failure", "timeout"), now)).toBe("job.no_success:2");
  expect(lastSuccessText(t, runs("success", "failure"), now)).toBe("job.last_success:09:54:job.ago:time.minutes:1");
  expect(lastSuccessText(t, runs("in_progress"), now)).toBeNull();
  expect(lastSuccessText(t, [], now)).toBeNull();
});

test("lastSuccessText dit « passage signalé » quand le ping n'a pas de code de sortie", () => {
  expect(lastSuccessText(t, runsAgo([["missed", 1 * hour], ["success", 5 * hour]], false), now)).toBe("job.last_signaled:04:55:job.ago:time.hours:5");
});

test("jobFormOf passe les secondes du serveur en minutes du formulaire", () => {
  const form = jobFormOf({ ...job, machine_id: "machine-1", interval_seconds: 3600, grace_seconds: 600 });
  expect(form).toEqual({ name: "Renouvellement certbot", machineID: "machine-1", interval: "60", grace: "10" });
});

test("jobRequestOf renvoie les minutes que le serveur attend", () => {
  expect(jobRequestOf({ name: "sauvegarde", machineID: "", interval: "60", grace: "10" })).toEqual({
    name: "sauvegarde",
    machine_id: "",
    interval_minutes: 60,
    grace_minutes: 10,
  });
});

test("une tâche relue puis renvoyée garde son rythme", () => {
  const request = jobRequestOf(jobFormOf(job));
  expect(request.interval_minutes * 60).toBe(job.interval_seconds);
  expect(request.grace_minutes * 60).toBe(job.grace_seconds);
});

test("jobTones donne un ton à chaque état de tâche : le danger pour le retard et l'échec", () => {
  const statuses: JobStatus[] = ["new", "on_time", "started", "late", "failed", "paused"];
  expect(statuses.map((status) => jobTones[status])).toEqual(["neutral", "ok", "accent", "danger", "danger", "neutral"]);
});

test("jobsSubtitle compte les tâches et ce qui demande attention", () => {
  expect(jobsSubtitle(t, 0, 0)).toBe("jobs.subtitle_none");
  expect(jobsSubtitle(t, 1, 0)).toBe("jobs.subtitle_one");
  expect(jobsSubtitle(t, 1, 1)).toBe("jobs.subtitle_one_attention");
  expect(jobsSubtitle(t, 6, 0)).toBe("jobs.subtitle_ok:6");
  expect(jobsSubtitle(t, 6, 2)).toBe("jobs.subtitle_attention:6:2");
});

test("exitCodeTone ne signale qu'un code non nul", () => {
  expect(exitCodeTone(0)).toBe("neutral");
  expect(exitCodeTone(1)).toBe("danger");
  expect(exitCodeTone(137)).toBe("danger");
  expect(exitCodeTone(-1)).toBe("danger");
});
