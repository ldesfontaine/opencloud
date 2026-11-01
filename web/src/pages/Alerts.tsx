import { useSearchParams } from "react-router";

import { useAlerts } from "../api/alerts";
import { useDeclaredServices } from "../api/directory";
import { Button } from "../components/Button";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { alertFilterOf, alertsSubtitle, withAlertFilter, type AlertFilter } from "../lib/alerts";
import { Inbox } from "./alerts/Inbox";

// La page Alertes est une boîte de réception : une seule liste, les non vues
// en tête, des filtres plutôt que des onglets. Les canaux et les silences
// vivent sous Paramètres › Notifications. Tout en direct ; les filtres sont
// dans l'adresse.
export function Alerts() {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const filter = alertFilterOf(params);
  const open = useAlerts("open");
  const resolved = useAlerts("resolved");
  const services = useDeclaredServices();
  const serviceNames = new Map((services.data?.services ?? []).map((service) => [service.id, service.title !== "" ? service.title : service.name]));
  const counts = open.data?.counts;

  const choose = (field: keyof AlertFilter, value: string) => setParams((current) => withAlertFilter(current, field, value), { replace: true });
  return (
    <>
      <PageHead
        title={t("alerts.title")}
        subtitle={counts ? alertsSubtitle(t, counts.open, counts.unacknowledged) : ""}
        actions={
          <Button variant="ghost" icon="bell" to="/parametres#notifications">
            {t("alerts.settings_link")}
          </Button>
        }
      />
      <Loaded resource={open}>
        {(data) => (
          <Inbox
            open={data.alerts}
            resolved={resolved.data?.alerts ?? null}
            filter={filter}
            serviceNames={serviceNames}
            onFilter={choose}
            onClearFilter={() => setParams({}, { replace: true })}
          />
        )}
      </Loaded>
    </>
  );
}
