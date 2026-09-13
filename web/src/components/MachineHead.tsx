import { NavLink, useNavigate } from "react-router";

import type { Machine } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { seenAgo } from "../lib/time";
import { Select } from "./Field";
import { StatePill } from "./Pill";
import "./MachineHead.scss";

// En-tête de machine : sélecteur, pastille d'état, métadonnées séparées par
// des points ; « vu il y a » avance tout seul.
export function MachineHead({ current, all }: { current: Machine; all: Machine[] }) {
  const t = useT();
  const navigate = useNavigate();
  const now = useNow();
  return (
    <div className="machine-head">
      <Select icon="server" aria-label={t("machine.select")} value={current.id} onChange={(event) => void navigate(`/machines/${event.target.value}`)}>
        {all.map((machine) => (
          <option key={machine.id} value={machine.id}>
            {machine.name}
          </option>
        ))}
      </Select>
      <span className="machine-state">
        <StatePill online={current.online} />
        <span className="meta">
          {current.address !== "" && (
            <>
              <span className="mono">{current.address}</span>
              <span className="sep" />
            </>
          )}
          <span>{current.os}</span>
          <span className="sep" />
          <span>
            {t("machine.agent")} <span className="mono">{current.agent_version}</span>
          </span>
          <span className="sep" />
          <span>{seenAgo(t, current.last_seen_at, now)}</span>
        </span>
      </span>
    </div>
  );
}

export interface Tab {
  slug: string;
  key: string;
}

// Les onglets d'une machine : segment d'URL → clé de libellé.
export const machineTabs: Tab[] = [
  { slug: "", key: "tab.summary" },
  { slug: "services", key: "tab.services" },
  { slug: "domaines", key: "tab.domains" },
  { slug: "reseau", key: "tab.network" },
  { slug: "sauvegardes", key: "tab.backups" },
  { slug: "journaux", key: "tab.logs" },
  { slug: "reglages", key: "tab.settings" },
];

export function Tabs({ machineID }: { machineID: string }) {
  const t = useT();
  return (
    <nav className="tabs">
      {machineTabs.map((tab) => (
        <NavLink
          key={tab.slug}
          className={({ isActive }) => (isActive ? "tab active" : "tab")}
          to={tab.slug === "" ? `/machines/${machineID}` : `/machines/${machineID}/${tab.slug}`}
          end
        >
          {t(tab.key)}
        </NavLink>
      ))}
    </nav>
  );
}
