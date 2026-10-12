import type { Run, RunOutcome } from "../api/types";
import { useT } from "../i18n/context";
import { formatClock } from "../lib/time";
import "./UptimeBar.scss";

const stripTones: Record<RunOutcome, "ok" | "warn" | "danger" | "none"> = {
  in_progress: "none",
  success: "ok",
  failure: "danger",
  timeout: "warn",
};

// La frise des exécutions : un segment par exécution, de la plus ancienne
// à la plus récente, pour voir d'un coup d'œil si la tâche vient de casser
// ou casse depuis longtemps.
export function RunStrip({ runs }: { runs: Run[] }) {
  const t = useT();
  const oldestFirst = [...runs].reverse();
  return (
    <div className="ubar ubar-runs" role="img" aria-label={t("job.strip_title", runs.length)}>
      {oldestFirst.map((run) => {
        const at = run.completed_at ?? run.started_at;
        const outcome = t(`job.outcome_${run.outcome}`);
        return <span className={`ubar-seg ubar-${stripTones[run.outcome]}`} key={run.id} title={at === null ? outcome : `${outcome} · ${formatClock(at)}`} />;
      })}
    </div>
  );
}
