import { useParams } from "react-router";

import { useStatusComponents } from "../api/status";
import { Button } from "../components/Button";
import { Icon } from "../components/Icon";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { useT } from "../i18n/context";
import { NotFound } from "./NotFound";
import { ComponentsTab } from "./status/ComponentsTab";
import { IncidentsTab } from "./status/IncidentsTab";
import { StatusSettingsTab } from "./status/StatusSettingsTab";

const tabs = [
  { slug: "", key: "statuspage.tab_components" },
  { slug: "incidents", key: "statuspage.tab_incidents" },
  { slug: "reglages", key: "statuspage.tab_settings" },
];

// L'administration de la page de statut : ses composants, ses incidents,
// ses réglages, sous trois onglets. Ce que le public ne voit jamais se lit
// ici : les objets rattachés, leur état, ce qui est caché.
export function StatusAdmin() {
  const { tab = "" } = useParams();
  const t = useT();
  const components = useStatusComponents();
  if (!tabs.some((candidate) => candidate.slug === tab)) {
    return <NotFound />;
  }
  const publicURL = components.data?.public_url ?? "";
  return (
    <>
      <PageHead
        title={t("statuspage.title")}
        subtitle={publicURL !== "" ? t("statuspage.subtitle", publicURL) : ""}
        actions={
          <>
            {publicURL !== "" && (
              <a className="btn btn-secondary" href={publicURL} target="_blank" rel="noopener noreferrer">
                <Icon name="external" />
                {t("statuspage.open_public")}
              </a>
            )}
            {tab === "" && (
              <Button variant="primary" icon="plus" to="/page-statut/composants/nouveau">
                {t("statuspage.add_component")}
              </Button>
            )}
            {tab === "incidents" && (
              <Button variant="primary" icon="plus" to="/page-statut/incidents/nouveau">
                {t("statuspage.add_incident")}
              </Button>
            )}
          </>
        }
      />
      <TabNav tabs={tabs.map((entry) => ({ to: entry.slug === "" ? "/page-statut" : `/page-statut/${entry.slug}`, key: entry.key }))} />
      {tab === "" && <ComponentsTab components={components} />}
      {tab === "incidents" && <IncidentsTab />}
      {tab === "reglages" && <StatusSettingsTab />}
    </>
  );
}
