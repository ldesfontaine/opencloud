import { useAlerts } from "../../api/alerts";
import { Card, CardBody, CardHeader } from "../../components/Card";
import { Pill } from "../../components/Pill";
import { List } from "../../components/Table";
import { useT } from "../../i18n/context";
import { backupAlerts } from "../../lib/backupOverview";
import { AlertRow } from "../alerts/AlertRow";

// La supervision des sauvegardes vit ici (décision de Lucas du 01/11/2026) :
// les alertes ouvertes de ce module, avec leur geste, ou ce qui est surveillé
// quand il n'y en a pas. Les alertes sont celles de la boîte « Alertes » : les
// canaux et les silences s'y règlent.
export function WatchSection() {
  const t = useT();
  const alerts = useAlerts("open");
  const mine = backupAlerts(alerts.data?.alerts ?? []);
  return (
    <Card>
      <CardHeader
        title={t("backup.watch_title")}
        aside={
          <Pill tone={mine.length === 0 ? "ok" : "warn"} small>
            {mine.length === 0 ? t("backup.watch_none") : t("backup.watch_open", mine.length)}
          </Pill>
        }
      />
      {mine.length === 0 ? (
        <CardBody>
          <span className="secondary">{t("backup.watch_rules")}</span>
        </CardBody>
      ) : (
        <List>
          {/* Les sources sans plan se disent toutes pareil : une ligne courte chacune, le geste est le même (créer une sauvegarde). */}
          {[...mine].sort((a, b) => Number(a.kind === "source_unprotected") - Number(b.kind === "source_unprotected")).map((alert) => (
            <AlertRow key={alert.id} alert={alert} compact={alert.kind === "source_unprotected"} />
          ))}
        </List>
      )}
    </Card>
  );
}
