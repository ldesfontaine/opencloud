import { Link } from "react-router";

import { acknowledgeAlert, type Alert } from "../../api/alerts";
import { useRefresh } from "../../api/refresh";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Dot, Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useDiskThresholds, useT, type Translate } from "../../i18n/context";
import { alertStateTones, describeAlert, mutePathOf, objectPath, severityTones, stateOf } from "../../lib/alerts";
import { ago } from "../../lib/time";
import "./Alerts.scss";

// Une alerte : le point de sa gravité, le fait, la cause, le geste ; à
// droite sa machine, son état, son moment, et ce qu'on peut en faire.
export function AlertRow({
  alert,
  compact = false,
  selected,
  onSelect,
}: {
  alert: Alert;
  compact?: boolean;
  selected?: boolean;
  onSelect?: () => void;
}) {
  const t = useT();
  const now = useNow();
  const disks = useDiskThresholds();
  const { refresh } = useRefresh();
  const action = useAction();
  const text = describeAlert(t, alert, now, disks);
  const state = stateOf(alert);
  const path = objectPath(alert.object, alert.machine_id);
  // On relit même après un refus : l'alerte a pu être acquittée ou résolue
  // entre-temps, et la ligne doit le dire.
  const acknowledge = () => {
    action.run(() => acknowledgeAlert(alert.id), { onSettled: () => refresh("alerts") });
  };
  return (
    <div className="row alert-row">
      {onSelect !== undefined && (
        <input className="alert-row-check" type="checkbox" checked={selected === true} onChange={onSelect} aria-label={t("alerts.select_alert", `${text.fact} · ${alert.object.name}`)} />
      )}
      <Dot tone={severityTones[alert.severity]} />
      <span className="txt">
        <Link to={`/alertes/alerte/${alert.id}`}>
          <b>{text.fact}</b>
        </Link>
        <span>{text.cause}</span>
        {!compact && <span className="action">{text.action}</span>}
      </span>
      <span className="aside">
        <span className="secondary">
          {path !== null ? <Link to={path}>{alert.object.name}</Link> : alert.object.name}
          {alert.machine_name !== "" && alert.object.kind !== "machine" && (
            <>
              {" · "}
              <Link to={`/machines/${alert.machine_id}`}>{alert.machine_name}</Link>
            </>
          )}
        </span>
        <span className="cluster">
          {alert.muted && (
            <Pill tone="neutral" dot={false}>
              {t("alert.muted")}
            </Pill>
          )}
          <Pill tone={alertStateTones[state]}>{t(`alert.status_${state}`)}</Pill>
          <span className="secondary nowrap">{when(t, alert, now)}</span>
        </span>
        <Failure error={action.error} />
        {!compact && state !== "resolved" && (
          <span className="actions">
            {state === "open" && (
              <Button small icon="check" onClick={acknowledge} disabled={action.busy}>
                {t("alert.acknowledge")}
              </Button>
            )}
            <Button small variant="ghost" to={mutePathOf(alert)}>
              {t("alerts.silence")}
            </Button>
          </span>
        )}
      </span>
    </div>
  );
}

function when(t: Translate, alert: Alert, now: number): string {
  if (alert.resolved_at !== null) {
    return t("alert.resolved_ago", ago(t, alert.resolved_at, now));
  }
  if (alert.acknowledged_at !== null) {
    return t("alert.acknowledged_ago", ago(t, alert.acknowledged_at, now));
  }
  return t("alert.opened_ago", ago(t, alert.opened_at, now));
}
