import type { Job, Run, RunOutcome } from "../api/types";
import type { Translate } from "../i18n/context";
import type { Tone } from "../components/Pill";
import { ago, formatDuration, formatWhen, since } from "./time";

// Le dernier résultat connu : code de sortie et durée, quand la tâche les
// a donnés.
export function lastResult(t: Translate, job: Job): { text: string; tone: "ok" | "danger" } | null {
  if (job.last_exit_code === null && job.last_duration_ms === null) {
    return null;
  }
  const parts: string[] = [];
  let tone: "ok" | "danger" = "ok";
  if (job.last_exit_code !== null) {
    parts.push(t("job.exit_code", job.last_exit_code));
    if (job.last_exit_code !== 0) {
      tone = "danger";
    }
  }
  if (job.last_duration_ms !== null) {
    parts.push(formatDuration(t, job.last_duration_ms));
  }
  return { text: parts.join(" · "), tone };
}

export const runTones: Record<RunOutcome, Tone> = {
  in_progress: "accent",
  success: "ok",
  failure: "danger",
  timeout: "warn",
};

// Ce qu'on montre du corps d'un ping : le début, sur une ligne.
const payloadPreview = 160;

export function preview(payload: string): string {
  const line = payload.split("\n", 1)[0] ?? "";
  const runes = Array.from(line);
  if (runes.length > payloadPreview) {
    return `${runes.slice(0, payloadPreview).join("")}…`;
  }
  return line;
}

export const attentionStatuses = new Set(["late", "failed"]);

// Une tâche qui n'a jamais pingé reste à installer : l'URL et les extraits
// sont ce que l'opérateur vient chercher.
export function awaitsFirstPing(job: Job): boolean {
  return job.last_ping_at === null;
}

// Le serveur ne rend que les vingt dernières exécutions (jobRunsShown) :
// une série qui les couvre toutes est peut-être plus longue.
export const runHistoryLimit = 20;

// Ce qu'on montre d'abord dans la liste ; le reste se déplie.
export const runsShownFirst = 5;

// La série en cours : combien d'exécutions finies se suivent avec la même
// issue, en partant de la plus récente. Une exécution dépassée compte comme
// un échec ; celle en cours ne compte pas.
export interface Streak {
  succeeded: boolean;
  count: number;
  isOpenEnded: boolean;
  since: string | null;
}

export function currentStreak(runs: Run[]): Streak | null {
  const finished = runs.filter((run) => run.outcome !== "in_progress");
  const latest = finished[0];
  if (latest === undefined) {
    return null;
  }
  const succeeded = latest.outcome === "success";
  let count = 0;
  for (const run of finished) {
    if ((run.outcome === "success") !== succeeded) {
      break;
    }
    count += 1;
  }
  const oldest = finished[count - 1];
  return {
    succeeded,
    count,
    isOpenEnded: count === finished.length && runs.length >= runHistoryLimit,
    since: oldest === undefined ? null : (oldest.started_at ?? oldest.completed_at),
  };
}

function streakFact(t: Translate, streak: Streak, now: number): string {
  if (streak.count === 1) {
    return streak.succeeded ? t("job.fact_success") : t("job.fact_failed");
  }
  if (streak.succeeded) {
    return streak.isOpenEnded ? t("job.fact_successes_all", streak.count) : t("job.fact_successes", streak.count);
  }
  if (streak.isOpenEnded || streak.since === null) {
    return t("job.fact_failures_all", streak.count);
  }
  return t("job.fact_failures", streak.count, formatWhen(streak.since, now));
}

// Le fait en tête de page : ce que la tâche fait en ce moment, en une
// phrase. L'état décide ; la série d'exécutions précise quand elle parle.
export function jobFact(t: Translate, job: Job, runs: Run[], now: number): string {
  const streak = currentStreak(runs);
  switch (job.status) {
    case "new":
      return t("job.fact_new");
    case "paused":
      return t("job.fact_paused");
    case "started":
      return job.run_started_at === null ? t("job.status_started") : t("job.fact_started", formatDuration(t, since(job.run_started_at, now)));
    case "late":
      return job.last_ping_at === null ? t("job.fact_never_pinged") : t("job.fact_late", formatDuration(t, since(job.last_ping_at, now)));
    case "failed":
      return streak === null || streak.succeeded ? t("job.fact_failed") : streakFact(t, streak, now);
    case "on_time":
      return streak === null || !streak.succeeded ? t("job.fact_on_time") : streakFact(t, streak, now);
  }
}

// Sous le fait, quand la tâche va mal : la dernière fois qu'elle a réussi,
// ou qu'aucune des exécutions connues n'a réussi.
export function lastSuccessText(t: Translate, job: Job, runs: Run[], now: number): string | null {
  if (!attentionStatuses.has(job.status)) {
    return null;
  }
  const finished = runs.filter((run) => run.outcome !== "in_progress");
  if (finished.length === 0) {
    return null;
  }
  const success = finished.find((run) => run.outcome === "success");
  const at = success === undefined ? null : (success.completed_at ?? success.started_at);
  if (at === null) {
    return t("job.no_success", finished.length);
  }
  return t("job.last_success", formatWhen(at, now), ago(t, at, now));
}
