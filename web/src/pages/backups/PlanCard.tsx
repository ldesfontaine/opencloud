import { deleteBackupPlan, runBackupNow, type BackupPlan, type Backups } from "../../api/backups";
import { reasonText } from "../../components/ActionsCard";
import { AgentGate } from "../../components/AgentGate";
import { Button } from "../../components/Button";
import { ConfirmModal } from "../../components/ConfirmModal";
import { Card, CardBody, CardHeader, KeyValue, Note } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { MissingRightButton } from "../../components/MissingRight";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { editPlanPath, lastErrorRight, lastErrorText, passSummary, planTitle, sourceLabel } from "../../lib/backups";
import type { Tone } from "../../lib/tone";
import { formatWhen } from "../../lib/time";

const stateTones: Record<string, Tone> = { sent: "neutral", accepted: "accent", succeeded: "ok", failed: "danger", refused: "danger", expired: "warn", queued: "neutral" };

// Un plan : d'où part la sauvegarde, où elle va, combien en garder, et le
// dernier ordre tel que le plan le garde. « Sauvegarder maintenant » la
// lance tout de suite ; un plan dont la source n'est pas prête dit pourquoi.
// Un plan agent passe machine virtuelle par machine virtuelle : combien sont
// arrivées, combien ont échoué, et le premier souci avec sa cible.
export function PlanCard({ plan, overview }: { plan: BackupPlan; overview: Backups }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  const run = plan.last_run;
  const destination = overview.destinations.find((candidate) => candidate.id === plan.destination_id);
  const output = run === null ? "" : run.output.map((line) => line.line).join("\n");
  const title = planTitle(plan, t("backup.source_kind_database"));
  const pass = passSummary(t, plan);
  const missing = lastErrorRight(plan, destination);
  const stop = () => {
    action.run(() => deleteBackupPlan(plan.subject), { topic: "backups" });
  };
  return (
    <Card>
      <CardHeader
        title={title}
        aside={
          <AgentGate machineIDs={[plan.source_machine_id, destination?.machine_id ?? ""].filter((id) => id !== "")}>
            {(offline) => (
              <Button small icon="archive" disabled={action.busy || offline} onClick={() => action.run(() => runBackupNow(plan.subject), { topic: "backups" })}>
                {t("backup.run_now")}
              </Button>
            )}
          </AgentGate>
        }
      />
      <CardBody gap={10}>
        <Failure error={action.error} />
        {plan.last_error !== "" && (
          <Note tone="danger">
            <p>{t("backup.last_error", lastErrorText(t, plan.last_error, (reason) => reasonText(t, reason)))}</p>
            {missing !== null && <MissingRightButton machineID={missing.machineID} right={missing.right} path={missing.path} />}
          </Note>
        )}
        <KeyValue label={t("backup.plan_source")} value={sourceLabel(t, plan)} />
        <KeyValue label={t("backup.plan_destination_label")} value={destination === undefined ? "" : `${destination.name} · ${destination.machine_name}`} />
        <KeyValue label={t("backup.plan_keep_label")} value={plan.keep} />
        {/* La tentative, pas l'ordre : un passage dont aucune cible n'est arrivée
            n'a pas d'ordre de réception, mais il a eu lieu. */}
        <KeyValue label={t("backup.last_attempt")} value={plan.last_attempt_at === null ? t("backup.last_none") : formatWhen(plan.last_attempt_at, now)} />
        {pass !== null && <KeyValue label={t("backup.last_pass")} value={pass} />}
        {run !== null && (
          <div>
            <Pill tone={stateTones[run.state] ?? "neutral"}>{t(`action.state_${run.state}`)}</Pill>
            {run.reason !== "" && <div className="secondary">{reasonText(t, run.reason)}</div>}
            {output !== "" && <pre className="secondary">{output}</pre>}
          </div>
        )}
        <p className="secondary">{t("backup.schedule_daily")}</p>
        <div className="cluster">
          <Button small variant="ghost" to={editPlanPath(plan.subject)}>
            {t("backup.plan_edit")}
          </Button>
          <ConfirmModal
            title={t("backup.plan_delete")}
            question={t("backup.plan_delete_confirm", title)}
            confirmLabel={t("backup.plan_delete")}
            onConfirm={stop}
            variant="ghost"
            small
            icon="x"
            disabled={action.busy}
          >
            {t("backup.plan_delete")}
          </ConfirmModal>
        </div>
      </CardBody>
    </Card>
  );
}
