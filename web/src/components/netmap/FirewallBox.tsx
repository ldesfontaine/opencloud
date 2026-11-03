import { Link } from "react-router";

import type { MapFirewall, MapZone } from "../../api/network";
import { useT } from "../../i18n/context";
import { interfaceAnchorID } from "../../lib/netmap";
import { Icon } from "../Icon";
import { Pill } from "../Pill";

// Le pare-feu déclaré, entre Internet et les zones : une ligne par interface,
// à la teinte de la zone qu'elle dessert. Un connecteur la relie à sa zone ;
// l'interface publique et celle d'un tunnel n'en ont pas.
export function FirewallBox({ firewall, zones }: { firewall: MapFirewall; zones: ReadonlyMap<string, MapZone> }) {
  const t = useT();
  return (
    <aside className="netmap-firewall" aria-label={firewall.name}>
      <header className="netmap-firewall-head">
        <Icon name="firewall" />
        <Link className="netmap-box-name" to={`/pare-feu/${firewall.id}`}>
          {firewall.name}
        </Link>
      </header>
      <ul className="netmap-interfaces" aria-label={t("network.map_firewall_zones")}>
        {firewall.interfaces.map((iface) => {
          const zone = zones.get(iface.zone_id);
          return (
            <li key={iface.name} className={`netmap-interface${zone === undefined ? "" : ` zone-tone-${zone.tone}`}${iface.up ? "" : " is-down"}`} data-anchor={interfaceAnchorID(firewall.id, iface.name)}>
              <span className="netmap-interface-name">
                {iface.tunnel && <Icon name="lock" />}
                {iface.role !== "" ? iface.role : iface.name}
              </span>
              <span className="netmap-interface-device mono">{iface.device}</span>
              {iface.cidr !== "" && <span className="netmap-interface-cidr mono">{iface.cidr}</span>}
              {iface.wan && (
                <Pill tone="neutral" dot={false} small>
                  {t("network.map_wan")}
                </Pill>
              )}
              {!iface.up && <span className="secondary">{t("network.map_interface_down")}</span>}
            </li>
          );
        })}
      </ul>
    </aside>
  );
}
