import type { Run, RunOutcome } from "../api/jobs";
import { useT } from "../i18n/context";
import { outcomeKey, runMoment } from "../lib/jobs";
import { formatClock } from "../lib/time";
import type { SegmentTone } from "../lib/tone";
import "./UptimeBar.scss";
import "./RunStrip.scss";

// Un retard n'est pas une exécution : « gap » le dessine en creux.
const stripTones: Record<RunOutcome, SegmentTone | "gap"> = {
  in_progress: "none",
  success: "ok",
  failure: "danger",
  timeout: "warn",
  missed: "gap",
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
        const at = runMoment(run);
        const outcome = t(outcomeKey(run));
        return <span className={`ubar-seg ubar-${stripTones[run.outcome]}`} key={run.id} title={at === null ? outcome : `${outcome} · ${formatClock(at)}`} />;
      })}
    </div>
  );
}
