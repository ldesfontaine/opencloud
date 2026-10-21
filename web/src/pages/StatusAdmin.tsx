import { useParams, useSearchParams } from "react-router";

import { useStatusPages } from "../api/status";
import { Button } from "../components/Button";
import { Icon } from "../components/Icon";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { useT } from "../i18n/context";
import { chosenPageID } from "../lib/status";
import { NotFound } from "./NotFound";
import { ComponentsTab } from "./status/ComponentsTab";
import { IncidentsTab } from "./status/IncidentsTab";
import { PagesTab } from "./status/PagesTab";

const tabs = [
  { slug: "", key: "statuspage.tab_pages" },
  { slug: "composants", key: "statuspage.tab_components" },
  { slug: "incidents", key: "statuspage.tab_incidents" },
];

// L'administration des pages de statut : les pages, les composants de
// chacune, les incidents, sous trois onglets. Ce que le public ne voit
// jamais se lit ici : les objets rattachés, leur état, ce qui est caché.
// L'onglet des composants montre une page à la fois, choisie par ?page=.
export function StatusAdmin() {
  const { tab = "" } = useParams();
  const [params] = useSearchParams();
  const t = useT();
  const pages = useStatusPages();
  const pageID = chosenPageID(pages.data?.pages ?? [], params.get("page"));
  const page = pages.data?.pages.find((candidate) => candidate.id === pageID) ?? null;
  if (!tabs.some((candidate) => candidate.slug === tab)) {
    return <NotFound />;
  }
  return (
    <>
      <PageHead
        title={t("statuspage.title")}
        subtitle={t("statuspage.subtitle")}
        actions={
          <>
            {tab === "composants" && page !== null && (
              <a className="btn btn-secondary" href={page.public_url} target="_blank" rel="noopener noreferrer">
                <Icon name="external" />
                {t("statuspage.open_public")}
              </a>
            )}
            {tab === "" && (
              <Button variant="primary" icon="plus" to="/page-statut/pages/nouvelle">
                {t("statuspage.add_page")}
              </Button>
            )}
            {tab === "composants" && page !== null && (
              <Button variant="primary" icon="plus" to={`/page-statut/composants/nouveau?page=${encodeURIComponent(page.id)}`}>
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
      {tab === "" && <PagesTab pages={pages} />}
      {tab === "composants" && <ComponentsTab pages={pages} pageID={pageID} />}
      {tab === "incidents" && <IncidentsTab />}
    </>
  );
}
