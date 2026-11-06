import { Link } from "react-router";

import type { MapFirewall } from "../../api/network";
import { useT } from "../../i18n/context";
import { interfaceAnchorID } from "../../lib/netmap";
import { Icon } from "../Icon";

// Le pare-feu déclaré, en bandeau : les tunnels (à gauche, du côté d'où le trait
// du tunnel arrive), son nom, le WAN et le nombre de zones qu'il dessert. Les
// zones elles-mêmes sont les couloirs dessous.
export function FirewallBox({ firewall }: { firewall: MapFirewall }) {
  const t = useT();
  const wans = firewall.interfaces.filter((iface) => iface.wan);
  const tunnels = firewall.interfaces.filter((iface) => iface.tunnel);
  const zones = firewall.interfaces.filter((iface) => iface.zone_id !== "").length;
  return (
    <aside className="netmap-firewall" aria-label={firewall.name}>
      {tunnels.length > 0 && (
        <ul className="netmap-firewall-chips">
          {tunnels.map((iface) => (
            <li key={iface.name} className={`netmap-fw-chip is-tunnel${iface.up ? "" : " is-down"}`} data-anchor={interfaceAnchorID(firewall.id, iface.name)}>
              <Icon name="lock" />
              <span className="netmap-fw-chip-name">{iface.role !== "" ? iface.role : iface.name}</span>
              <span className="mono">{iface.device}</span>
              {!iface.up && <span className="secondary">{t("network.map_interface_down")}</span>}
            </li>
          ))}
        </ul>
      )}
      <Icon name="firewall" />
      <Link className="netmap-box-name" to={`/pare-feu/${firewall.id}`}>
        {firewall.name}
      </Link>
      {wans.length > 0 && (
        <ul className="netmap-firewall-chips">
          {wans.map((iface) => (
            <li key={iface.name} className={`netmap-fw-chip${iface.up ? "" : " is-down"}`}>
              <span className="netmap-fw-chip-name">{t("network.map_wan")}</span>
              <span className="mono">{iface.cidr}</span>
              {!iface.up && <span className="secondary">{t("network.map_interface_down")}</span>}
            </li>
          ))}
        </ul>
      )}
      <span className="netmap-firewall-zones secondary">{t(zones === 1 ? "network.map_firewall_zones_one" : "network.map_firewall_zones", zones)}</span>
    </aside>
  );
}
