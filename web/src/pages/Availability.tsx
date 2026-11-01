import { Outlet } from "react-router";

import { ActionButton } from "../components/ActionButton";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { useT } from "../i18n/context";
import { newProbePath } from "../lib/probes";
import { availabilityPath, incidentsPath, newIncidentPath, publicPagesPath } from "../lib/status";
import { IncidentsTab } from "./availability/IncidentsTab";
import { PagesTab } from "./availability/PagesTab";
import { ProbesTab } from "./availability/ProbesTab";

export type AvailabilityTab = "probes" | "pages" | "incidents";

const tabs = [
  { to: availabilityPath, key: "availability.tab_probes" },
  { to: publicPagesPath, key: "availability.tab_pages" },
  { to: incidentsPath, key: "availability.tab_incidents" },
];

// La page Disponibilité : ce qu'openCloud vérifie depuis l'extérieur (les
// sondes), ce que le public lit (la page de chaque projet publié) et les
// incidents que l'opérateur lui dit. Une seule rangée d'onglets ; l'action
// principale de l'en-tête est la création de l'objet de l'onglet.
export function Availability({ tab }: { tab: AvailabilityTab }) {
  const t = useT();
  return (
    <>
      <PageHead title={t("module.probes")} subtitle={t("availability.subtitle")} actions={<TabAction tab={tab} />} />
      <TabNav tabs={tabs} />
      {tab === "probes" && <ProbesTab />}
      {tab === "pages" && <PagesTab />}
      {tab === "incidents" && <IncidentsTab />}
      {/* La fenêtre de l'adresse : ajouter une sonde. */}
      <Outlet />
    </>
  );
}

function TabAction({ tab }: { tab: AvailabilityTab }) {
  const t = useT();
  switch (tab) {
    case "probes":
      return (
        <ActionButton route="POST /api/probes" icon="plus" to={newProbePath()}>
          {t("probe.add")}
        </ActionButton>
      );
    case "pages":
      // Un projet se publie depuis son assistant, dans Services.
      return (
        <ActionButton route="PUT /api/directory/projects/{id}" icon="eye" to="/services/projets">
          {t("availability.publish_project")}
        </ActionButton>
      );
    case "incidents":
      return (
        <ActionButton route="POST /api/status/incidents" icon="plus" to={newIncidentPath}>
          {t("availability.add_incident")}
        </ActionButton>
      );
  }
}
