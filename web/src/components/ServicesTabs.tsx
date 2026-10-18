import { NavLink } from "react-router";

import { useT } from "../i18n/context";

const tabs = [
  { href: "/services", key: "services.tab_containers" },
  { href: "/services/annuaire", key: "services.tab_directory" },
  { href: "/services/projets", key: "services.tab_projects" },
];

export function ServicesTabs() {
  const t = useT();
  return (
    <nav className="tabs">
      {tabs.map((entry) => (
        <NavLink key={entry.href} className={({ isActive }) => (isActive ? "tab active" : "tab")} to={entry.href} end>
          {t(entry.key)}
        </NavLink>
      ))}
    </nav>
  );
}
