import type { Job, RunOutcome } from "../api/types";
import type { Translate } from "../i18n/context";
import type { Tone } from "../components/Pill";
import { formatDuration } from "./time";

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
