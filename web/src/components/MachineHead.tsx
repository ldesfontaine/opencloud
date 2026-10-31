import { useNavigate, useParams } from "react-router";

import type { Machine } from "../api/machines";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { agentBehindServer } from "../lib/agentUpdate";
import { machineSeen } from "../lib/machines";
import { machineTabsOf, type ModuleID } from "../lib/modules";
import { MachineSelect } from "./MachineSelect";
import { MachineStatePill } from "./Pill";
import { SupportLine } from "./SupportBlock";
import { TabNav } from "./TabNav";
import "./MachineHead.scss";

// En-tête de machine : sélecteur, pastille d'état, métadonnées séparées par
// des points ; « vu il y a » avance tout seul. Dessous, sur une ligne, la fin
// de support du système quand elle est activée (le détail est sous l'historique).
export function MachineHead({ current, all }: { current: Machine; all: Machine[] }) {
  const { t, version } = useI18n();
  const navigate = useNavigate();
  const { tab = "" } = useParams();
  const now = useNow();
  // Changer de machine garde l'onglet : on compare deux machines sur la
  // même vue.
  const pathOf = (id: string) => (tab === "" ? `/machines/${id}` : `/machines/${id}/${tab}`);
  return (
    <div className="machine-head">
      <MachineSelect icon="server" aria-label={t("machine.select")} machines={all} value={current.id} onChange={(id) => void navigate(pathOf(id))} />
      <span className="machine-state">
        <MachineStatePill machine={current} />
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
            {current.agent !== null && current.agent.revoked_at === null && agentBehindServer(current.agent.version, version) && (
              <> · {t("machine.agent_behind", version)}</>
            )}
          </span>
          <span className="sep" />
          <span>{machineSeen(t, current, now)}</span>
        </span>
      </span>
      {current.lifecycle !== undefined && <SupportLine lifecycle={current.lifecycle} subject="system" />}
    </div>
  );
}

export function Tabs({ machineID, modules }: { machineID: string; modules: readonly ModuleID[] }) {
  const tabs = machineTabsOf(modules).map((tab) => ({ to: tab.slug === "" ? `/machines/${machineID}` : `/machines/${machineID}/${tab.slug}`, key: tab.label }));
  return <TabNav tabs={tabs} />;
}
