import { expect, test } from "vitest";

import type { Job, Run, RunOutcome } from "../api/types";
import type { Translate } from "../i18n/context";
import { awaitsFirstPing, currentStreak, jobFact, lastSuccessText, runHistoryLimit } from "./jobs";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

const now = new Date(2026, 9, 12, 9, 55, 0).getTime();

function at(hour: number, minute: number): string {
  return new Date(2026, 9, 12, hour, minute, 0).toISOString();
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
    started_at: at(9, 54 - index),
    completed_at: outcome === "in_progress" ? null : at(9, 54 - index),
    duration_ms: 2000,
    exit_code: outcome === "failure" ? 1 : 0,
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

test("jobFact dit la série de réussites d'une tâche à l'heure", () => {
  const onTime: Job = { ...job, status: "on_time", last_ping_at: at(9, 54) };
  expect(jobFact(t, onTime, runs("success", "success", "success", "failure"), now)).toBe("job.fact_successes:3");
  expect(jobFact(t, onTime, runs("success", "failure"), now)).toBe("job.fact_success");
  expect(jobFact(t, onTime, runs(...Array<RunOutcome>(runHistoryLimit).fill("success")), now)).toBe(`job.fact_successes_all:${runHistoryLimit}`);
  expect(jobFact(t, onTime, [], now)).toBe("job.fact_on_time");
});

test("jobFact dit le retard, l'attente, la pause et l'exécution en cours", () => {
  expect(jobFact(t, job, [], now)).toBe("job.fact_new");
  expect(jobFact(t, { ...job, status: "late" }, [], now)).toBe("job.fact_never_pinged");
  expect(jobFact(t, { ...job, status: "late", last_ping_at: at(9, 47) }, runs("success"), now)).toBe("job.fact_late:time.minutes:8");
  expect(jobFact(t, { ...job, status: "paused" }, runs("success"), now)).toBe("job.fact_paused");
  expect(jobFact(t, { ...job, status: "started", run_started_at: at(9, 52) }, runs("in_progress"), now)).toBe("job.fact_started:time.minutes:3");
});

test("lastSuccessText ne parle que d'une tâche qui va mal", () => {
  const failed: Job = { ...job, status: "failed", last_ping_at: at(9, 54) };
  expect(lastSuccessText(t, failed, runs("failure", "failure", "success"), now)).toBe("job.last_success:09:52:job.ago:time.minutes:3");
  expect(lastSuccessText(t, failed, runs("failure", "timeout"), now)).toBe("job.no_success:2");
  expect(lastSuccessText(t, failed, [], now)).toBeNull();
  expect(lastSuccessText(t, { ...job, status: "on_time" }, runs("success"), now)).toBeNull();
});
