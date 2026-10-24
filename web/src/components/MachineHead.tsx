import { useNavigate, useParams } from "react-router";

import type { Machine } from "../api/machines";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { visibleMachineTabs } from "../lib/machineTabs";
import { machineSeen } from "../lib/machines";
import { Select } from "./Field";
import { OnlinePill } from "./Pill";
import { SupportLine } from "./SupportBlock";
import { TabNav } from "./TabNav";
import "./MachineHead.scss";

// En-tête de machine : sélecteur, pastille d'état, métadonnées séparées par
// des points ; « vu il y a » avance tout seul. Dessous, sur une ligne, la fin
// de support du système quand elle est activée (le détail est sous l'historique).
export function MachineHead({ current, all }: { current: Machine; all: Machine[] }) {
  const t = useT();
  const navigate = useNavigate();
  const { tab = "" } = useParams();
  const now = useNow();
  // Changer de machine garde l'onglet : on compare deux machines sur la
  // même vue.
  const pathOf = (id: string) => (tab === "" ? `/machines/${id}` : `/machines/${id}/${tab}`);
  return (
    <div className="machine-head">
      <Select icon="server" aria-label={t("machine.select")} value={current.id} onChange={(event) => void navigate(pathOf(event.target.value))}>
        {all.map((machine) => (
          <option key={machine.id} value={machine.id}>
            {machine.name}
          </option>
        ))}
      </Select>
      <span className="machine-state">
        <OnlinePill online={current.online} />
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
            {t("machine.agent")} <span className="mono">{current.agent?.version ?? "—"}</span>
          </span>
          <span className="sep" />
          <span>{machineSeen(t, current, now)}</span>
        </span>
      </span>
      {current.lifecycle !== undefined && <SupportLine lifecycle={current.lifecycle} subject="system" />}
    </div>
  );
}

export function Tabs({ machineID, hostsProxmox }: { machineID: string; hostsProxmox: boolean }) {
  const tabs = visibleMachineTabs(hostsProxmox).map((tab) => ({ to: tab.slug === "" ? `/machines/${machineID}` : `/machines/${machineID}/${tab.slug}`, key: tab.key }));
  return <TabNav tabs={tabs} />;
}
