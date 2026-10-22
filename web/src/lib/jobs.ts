import type { Job, JobRequest, JobStatus, Run, RunOutcome } from "../api/jobs";
import type { Translate } from "../i18n/context";
import { ago, formatDuration, formatWhen, since } from "./time";
import type { Tone } from "./tone";

// Le ton d'un code de sortie : seul un code non nul est un fait à signaler ;
// zéro n'est pas une bonne nouvelle à teinter, la pastille de l'état le dit.
export function exitCodeTone(code: number): Tone {
  return code === 0 ? "neutral" : "danger";
}

// Le dernier résultat connu : code de sortie et durée, quand la tâche les
// a donnés.
export function lastResult(t: Translate, job: Job): { text: string; tone: Tone } | null {
  if (job.last_exit_code === null && job.last_duration_ms === null) {
    return null;
  }
  const parts: string[] = [];
  if (job.last_exit_code !== null) {
    parts.push(t("job.exit_code", job.last_exit_code));
  }
  if (job.last_duration_ms !== null) {
    parts.push(formatDuration(t, job.last_duration_ms));
  }
  return { text: parts.join(" · "), tone: job.last_exit_code === null ? "neutral" : exitCodeTone(job.last_exit_code) };
}

// Pastille par état de tâche : le vocabulaire de la direction artistique.
export const jobTones: Record<JobStatus, Tone> = {
  new: "neutral",
  on_time: "ok",
  started: "accent",
  late: "danger",
  failed: "danger",
  paused: "neutral",
};

export const runTones: Record<RunOutcome, Tone> = {
  in_progress: "accent",
  success: "ok",
  failure: "danger",
  timeout: "warn",
  missed: "warn",
};

// Le moment d'une exécution : sa fin, ou son début tant qu'elle court.
export function runMoment(run: Run): string | null {
  return run.completed_at ?? run.started_at;
}

// Un ping nu (sans code de sortie) part même si la commande d'avant a
// échoué : sa « réussite » est un passage signalé, pas un succès constaté.
export function isSignaled(run: Run): boolean {
  return run.outcome === "success" && run.exit_code === null;
}

// La clé du libellé d'une exécution.
export function outcomeKey(run: Run): string {
  return isSignaled(run) ? "job.outcome_signaled" : `job.outcome_${run.outcome}`;
}

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

// La série en cours, en partant de l'exécution la plus récente. Une série de
// réussites s'arrête au premier « missed », échec ou dépassement ; une série
// d'échecs ne compte que les échecs et les dépassements (un « missed » n'est
// pas une exécution) et s'arrête à tout autre. L'exécution en cours ne compte
// pas.
export interface Streak {
  succeeded: boolean;
  count: number;
  isOpenEnded: boolean;
  // Où la série a commencé : le début d'une série d'échecs, la fin de la
  // première réussite d'une série de réussites.
  since: string | null;
  // Ce qui a coupé la série ; nul quand elle couvre tout l'historique rendu.
  brokenBy: RunOutcome | null;
  // Série de réussites : le dernier signal d'avant la rupture (réussite ou
  // échec, une exécution close par un ping), s'il est dans l'historique
  // rendu. Il donne la durée du trou : un échec est un signal, le trou part
  // de lui et pas de la réussite d'avant.
  beforeBreak: Run | null;
}

// Les exécutions finies : celle qui court encore n'a pas d'issue.
function finishedRuns(runs: Run[]): Run[] {
  return runs.filter((run) => run.outcome !== "in_progress");
}

export function currentStreak(runs: Run[]): Streak | null {
  const finished = finishedRuns(runs);
  const latest = finished[0];
  if (latest === undefined) {
    return null;
  }
  const succeeded = latest.outcome === "success";
  const continues = (run: Run) => (succeeded ? run.outcome === "success" : run.outcome === "failure" || run.outcome === "timeout");
  let count = 0;
  for (const run of finished) {
    if (!continues(run)) {
      break;
    }
    count += 1;
  }
  const oldest = finished[count - 1];
  const breaker = finished[count];
  let since: string | null = null;
  if (oldest !== undefined) {
    since = succeeded ? runMoment(oldest) : (oldest.started_at ?? oldest.completed_at);
  }
  return {
    succeeded,
    count,
    isOpenEnded: count === finished.length && runs.length >= runHistoryLimit,
    since,
    brokenBy: breaker === undefined ? null : breaker.outcome,
    beforeBreak: succeeded ? (finished.slice(count + 1).find((run) => run.outcome === "success" || run.outcome === "failure") ?? null) : null,
  };
}

const day = 24 * 60 * 60 * 1000;

// La phrase d'une série de réussites (tâche à l'heure). Une reprise récente
// après un « missed » dit le trou ; une rupture plus ancienne, ou un échec,
// dit depuis quand tout va bien.
function successFact(t: Translate, streak: Streak, finishedCount: number, now: number): string {
  if (finishedCount === 1) {
    return t("job.fact_success");
  }
  if (streak.since === null) {
    return t("job.fact_on_time");
  }
  const resumedFor = since(streak.since, now);
  if (streak.brokenBy === "missed" && resumedFor < day) {
    const lastBefore = streak.beforeBreak === null ? null : runMoment(streak.beforeBreak);
    if (lastBefore === null) {
      return t("job.fact_resumed_after_late", formatDuration(t, resumedFor));
    }
    const gap = Date.parse(streak.since) - Date.parse(lastBefore);
    return t("job.fact_resumed", formatDuration(t, resumedFor), formatDuration(t, gap));
  }
  if (streak.brokenBy === null) {
    return streak.isOpenEnded ? t("job.fact_on_time_at_least", formatDuration(t, resumedFor)) : t("job.fact_on_time_since", formatDuration(t, resumedFor));
  }
  return t("job.fact_on_time_since", formatDuration(t, resumedFor));
}

function failureFact(t: Translate, streak: Streak, now: number): string {
  if (streak.count === 1) {
    return t("job.fact_failed");
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
      return streak === null || streak.succeeded ? t("job.fact_failed") : failureFact(t, streak, now);
    case "on_time": {
      if (streak === null || !streak.succeeded) {
        return t("job.fact_on_time");
      }
      return successFact(t, streak, finishedRuns(runs).length, now);
    }
  }
}

// Sous le fait, toujours : la dernière fois que la tâche a réussi, ou
// qu'aucune des exécutions connues n'a réussi. Sans code de sortie, c'est un
// passage signalé, pas une réussite constatée.
export function lastSuccessText(t: Translate, runs: Run[], now: number): string | null {
  const finished = finishedRuns(runs);
  if (finished.length === 0) {
    return null;
  }
  const success = finished.find((run) => run.outcome === "success");
  const at = success === undefined ? null : runMoment(success);
  if (success === undefined || at === null) {
    return t("job.no_success", finished.length);
  }
  return t(isSignaled(success) ? "job.last_signaled" : "job.last_success", formatWhen(at, now), ago(t, at, now));
}

// Le formulaire d'une tâche, tel que l'opérateur le tape : le serveur parle
// en secondes, le formulaire en minutes.
export interface JobFormValues {
  name: string;
  machineID: string;
  interval: string;
  grace: string;
}

export const emptyJobForm: JobFormValues = { name: "", machineID: "", interval: "60", grace: "10" };

const secondsPerMinute = 60;

export function jobFormOf(job: Job): JobFormValues {
  return {
    name: job.name,
    machineID: job.machine_id,
    interval: String(Math.round(job.interval_seconds / secondsPerMinute)),
    grace: String(Math.round(job.grace_seconds / secondsPerMinute)),
  };
}

export function jobRequestOf(values: JobFormValues): JobRequest {
  return {
    name: values.name,
    machine_id: values.machineID,
    interval_minutes: Number(values.interval),
    grace_minutes: Number(values.grace),
  };
}

// Le sous-titre de la page des tâches : rien, ou le compte et ce qui
// demande attention.
export function jobsSubtitle(t: Translate, total: number, attention: number): string {
  if (total === 0) {
    return t("jobs.subtitle_none");
  }
  if (total === 1) {
    return t(attention === 0 ? "jobs.subtitle_one" : "jobs.subtitle_one_attention");
  }
  if (attention === 0) {
    return t("jobs.subtitle_ok", total);
  }
  return t("jobs.subtitle_attention", total, attention);
}
