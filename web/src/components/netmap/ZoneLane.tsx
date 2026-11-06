import type { ReactNode } from "react";

import type { MapZone } from "../../api/network";
import { useT } from "../../i18n/context";

// Le couloir d'une zone : son nom, son tag VLAN, son sous-réseau, à sa teinte,
// et les cartes des machines qui y sont. Le nom est toujours écrit : la teinte
// ne porte jamais seule.
export function ZoneLane({ zone, children }: { zone: MapZone | undefined; children: ReactNode }) {
  const t = useT();
  return (
    <section className={`netmap-lane${zone === undefined ? "" : ` zone-tone-${zone.tone}`}`} aria-label={zone?.name ?? t("network.map_no_zone")}>
      <header className="netmap-lane-head">
        <span className="netmap-zone-name">{zone?.name ?? t("network.map_no_zone")}</span>
        {zone !== undefined && zone.vlan > 0 && zone.source !== "vlan" && <span className="netmap-zone-meta mono">{t("network.map_vlan", zone.vlan)}</span>}
        {zone !== undefined && zone.cidr !== "" && <span className="netmap-zone-meta mono">{zone.cidr}</span>}
      </header>
      <div className="netmap-lane-cards">{children}</div>
    </section>
  );
}
