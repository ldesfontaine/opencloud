import { useEffect, useRef, useState } from "react";

import { acknowledgeAlerts, type Alert } from "../../api/alerts";
import { useRefresh } from "../../api/refresh";
import { Button } from "../../components/Button";
import { Card } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { List } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { acknowledgedMessage, coverage, groupByMachine, groupCountLabel, keepSelectable, selectableIDs, selectedLabel, toggleAll } from "../../lib/alertSelection";
import { AlertRow } from "./AlertRow";
import "./Alerts.scss";

// Les ouvertes à acquitter : une case par alerte, une par machine, « tout
// sélectionner », et l'acquittement groupé en une requête. La sélection se
// vide d'elle-même de ce qui a été acquitté ou résolu en direct.
export function SelectableAlerts({ alerts, notice: message, setNotice: setMessage }: { alerts: Alert[]; notice: string | null; setNotice: (notice: string | null) => void }) {
  const t = useT();
  const { refresh } = useRefresh();
  const action = useAction();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  useEffect(() => setSelected((current) => keepSelectable(current, alerts)), [alerts]);
  const all = selectableIDs(alerts);
  const toggle = (ids: number[]) => {
    setMessage(null);
    setSelected((current) => toggleAll(current, ids));
  };
  // Relire même après un refus : une partie de la sélection a pu passer, ou
  // être résolue entre-temps.
  const acknowledge = (ids: number[]) => {
    const perform = () => {
      setMessage(null);
      return acknowledgeAlerts(ids);
    };
    action.run(perform, {
      onDone: (response) => {
        setMessage(acknowledgedMessage(t, response.acknowledged));
        setSelected(new Set());
      },
      onSettled: () => refresh("alerts"),
    });
  };
  const groups = groupByMachine(alerts);
  return (
    <Card>
      <div className="alert-bulk">
        <label className="alert-check">
          <SelectBox state={coverage(selected, all)} onChange={() => toggle(all)} />
          <span>{t("alerts.select_all")}</span>
        </label>
        <span className="secondary" aria-live="polite">
          {selected.size > 0 && selectedLabel(t, selected.size)}
        </span>
        <span className="alert-bulk-actions">
          <Button small variant="primary" icon="check" onClick={() => acknowledge([...selected])} disabled={action.busy || selected.size === 0}>
            {t("alerts.acknowledge_selection")}
          </Button>
          <Button small icon="check" onClick={() => acknowledge(all)} disabled={action.busy || all.length === 0}>
            {t("alerts.acknowledge_all")}
          </Button>
        </span>
      </div>
      {(message !== null || action.error !== null) && (
        <div className="alert-bulk-result" role="status">
          {message !== null && <Pill tone="ok">{message}</Pill>}
          <Failure error={action.error} />
        </div>
      )}
      {groups.map((group) => {
        const ids = selectableIDs(group.alerts);
        const name = group.machineName === "" ? t("alert.no_machine") : group.machineName;
        return (
          <section className="alert-group" key={group.machineID || "none"}>
            <div className="alert-group-head">
              <label className="alert-check">
                <SelectBox state={coverage(selected, ids)} onChange={() => toggle(ids)} label={t("alerts.select_machine", name)} />
                <b>{name}</b>
              </label>
              <span className="secondary">{groupCountLabel(t, group.alerts.length)}</span>
            </div>
            <List>
              {group.alerts.map((alert) => (
                <AlertRow key={alert.id} alert={alert} selected={selected.has(alert.id)} onSelect={() => toggle([alert.id])} />
              ))}
            </List>
          </section>
        );
      })}
    </Card>
  );
}

// Une case qui couvre un ensemble : cochée, vide, ou indéterminée quand une
// partie seulement l'est ; l'état indéterminé ne se pose que par le DOM.
function SelectBox({ state, onChange, label }: { state: "all" | "some" | "none"; onChange: () => void; label?: string }) {
  const box = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (box.current) {
      box.current.indeterminate = state === "some";
    }
  }, [state]);
  return <input ref={box} type="checkbox" checked={state === "all"} onChange={onChange} aria-label={label} />;
}
