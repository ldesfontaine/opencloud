import { TabNav } from "./TabNav";

const tabs = [
  { to: "/services", key: "services.tab_directory" },
  { to: "/services/projets", key: "services.tab_projects" },
];

export function ServicesTabs() {
  return <TabNav tabs={tabs} />;
}
