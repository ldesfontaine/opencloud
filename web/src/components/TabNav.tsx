import { NavLink } from "react-router";

import { useT } from "../i18n/context";

export interface Tab {
  to: string;
  // La clé du catalogue de l'intitulé.
  key: string;
}

// Une barre d'onglets : un lien par onglet, actif sur son adresse exacte.
export function TabNav({ tabs }: { tabs: readonly Tab[] }) {
  const t = useT();
  return (
    <nav className="tabs">
      {tabs.map((tab) => (
        <NavLink key={tab.to} className={({ isActive }) => (isActive ? "tab active" : "tab")} to={tab.to} end>
          {t(tab.key)}
        </NavLink>
      ))}
    </nav>
  );
}
