import { useState } from "react";

import { useAlerts, type Alert } from "../../api/alerts";
import { Card, Empty } from "../../components/Card";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { List } from "../../components/Table";
import { useT } from "../../i18n/context";
import { AlertRow } from "./AlertRow";
import { SelectableAlerts } from "./SelectableAlerts";
import "./Alerts.scss";

// Une liste d'alertes : les ouvertes non acquittées, les acquittées, ou
// les résolues.
export function AlertsTab({ status, acknowledged }: { status: "open" | "resolved"; acknowledged: boolean | null }) {
  const alerts = useAlerts(status);
  // Le retour d'un acquittement groupé vit ici : il reste lisible même
  // quand la liste s'est vidée.
  const [notice, setNotice] = useState<string | null>(null);
  return (
    <Loaded resource={alerts}>
      {(data) => {
        const list = data.alerts.filter((alert) => acknowledged === null || (alert.acknowledged_at !== null) === acknowledged);
        return <AlertList alerts={list} status={status} acknowledged={acknowledged} notice={notice} setNotice={setNotice} />;
      }}
    </Loaded>
  );
}

function AlertList({
  alerts,
  status,
  acknowledged,
  notice,
  setNotice,
}: {
  alerts: Alert[];
  status: "open" | "resolved";
  acknowledged: boolean | null;
  notice: string | null;
  setNotice: (notice: string | null) => void;
}) {
  const t = useT();
  const toAcknowledge = status === "open" && !acknowledged;
  if (alerts.length === 0) {
    const text = status === "resolved" ? "alerts.empty_resolved_text" : acknowledged ? "alerts.empty_acknowledged_text" : "alerts.empty_open_text";
    // Le retour de l'acquittement groupé passe avant le message : EmptyCard
    // le mettrait dessous.
    return (
      <Card>
        {notice !== null && (
          <div className="alert-bulk-result" role="status">
            <Pill tone="ok">{notice}</Pill>
          </div>
        )}
        <Empty icon="bell" title={toAcknowledge ? t("alerts.empty_open_title") : undefined} text={t(text)} />
      </Card>
    );
  }
  if (toAcknowledge) {
    return <SelectableAlerts alerts={alerts} notice={notice} setNotice={setNotice} />;
  }
  return (
    <Card>
      <List>
        {alerts.map((alert) => (
          <AlertRow key={alert.id} alert={alert} />
        ))}
      </List>
    </Card>
  );
}
