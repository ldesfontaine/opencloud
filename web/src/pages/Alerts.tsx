import { useParams } from "react-router";

import { useAlerts } from "../api/alerts";
import { Button } from "../components/Button";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { useT } from "../i18n/context";
import { alertsSubtitle } from "../lib/alerts";
import { AlertsTab } from "./alerts/AlertsTab";
import { ChannelsTab } from "./alerts/ChannelsTab";
import { SilencesTab } from "./alerts/SilencesTab";
import { NotFound } from "./NotFound";

const tabs = [
  { slug: "", key: "alerts.tab_open" },
  { slug: "acquittees", key: "alerts.tab_acknowledged" },
  { slug: "resolues", key: "alerts.tab_resolved" },
  { slug: "canaux", key: "alerts.tab_channels" },
  { slug: "silences", key: "alerts.tab_silences" },
];

// La page Alertes : les ouvertes, les acquittées, les résolues, puis les
// canaux et les silences, sous cinq onglets. Tout en direct.
export function Alerts() {
  const { tab = "" } = useParams();
  const t = useT();
  const counts = useAlerts();
  if (!tabs.some((candidate) => candidate.slug === tab)) {
    return <NotFound />;
  }
  const open = counts.data?.counts.open ?? 0;
  const unacknowledged = counts.data?.counts.unacknowledged ?? 0;
  return (
    <>
      <PageHead
        title={t("alerts.title")}
        subtitle={counts.data ? alertsSubtitle(t, open, unacknowledged) : ""}
        actions={
          <>
            {tab === "canaux" && (
              <Button variant="primary" icon="plus" to="/alertes/canaux/nouveau">
                {t("channel.add")}
              </Button>
            )}
            {tab === "silences" && (
              <Button variant="primary" icon="plus" to="/alertes/silences/nouveau">
                {t("silence.add")}
              </Button>
            )}
          </>
        }
      />
      <TabNav tabs={tabs.map((entry) => ({ to: entry.slug === "" ? "/alertes" : `/alertes/${entry.slug}`, key: entry.key }))} />
      {tab === "" && <AlertsTab status="open" acknowledged={false} />}
      {tab === "acquittees" && <AlertsTab status="open" acknowledged={true} />}
      {tab === "resolues" && <AlertsTab status="resolved" acknowledged={null} />}
      {tab === "canaux" && <ChannelsTab />}
      {tab === "silences" && <SilencesTab />}
    </>
  );
}
