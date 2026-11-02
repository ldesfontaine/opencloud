import { useMemo, useState } from "react";

import { type NetworkMap as MapData, useNetworkMap } from "../../api/network";
import { useT } from "../../i18n/context";
import { allBoxes, homeAnchorID, initiallyFolded, internetAnchorID, isHost, isIdle, splitIdle, topBoxes } from "../../lib/netmap";
import { Button } from "../Button";
import { EmptyCard } from "../Card";
import { Failure } from "../Failure";
import { Icon } from "../Icon";
import { FirewallBox } from "./FirewallBox";
import { LinkLayer } from "./LinkLayer";
import { LinkList } from "./LinkList";
import { IdleLine, MachineBox } from "./MachineBox";
import "./NetworkMap.scss";

// La carte du réseau de la vue d'ensemble : le dehors à gauche, Internet au
// milieu, la maison à droite. Les machines sont des boîtes HTML qui se placent
// seules ; un calque SVG trace par-dessus ce que les données relient (liens,
// entrées d'Internet, tunnels, interfaces du pare-feu). Un hôte se replie sur
// son en-tête : à quarante machines, la carte s'ouvre repliée.
export function NetworkMap() {
  const t = useT();
  const network = useNetworkMap();
  const map = network.data;
  if (network.error && !map) {
    return <Failure error={network.error} />;
  }
  if (map && allBoxes(map).length === 0) {
    return <EmptyCard icon="network" text={t("network.overview_empty")} />;
  }
  return map ? <Board map={map} /> : <div className="netmap" aria-busy="true" />;
}

function Board({ map }: { map: MapData }) {
  const t = useT();
  // Le conteneur se garde en état : le calque des traits attend qu'il existe.
  const [root, setRoot] = useState<HTMLDivElement | null>(null);
  // Ce que l'opérateur a replié ou déplié à la main l'emporte sur le réglage de
  // départ, et un rafraîchissement n'y touche pas.
  const [chosen, setChosen] = useState<ReadonlyMap<string, boolean>>(new Map());
  const folded = useMemo(() => {
    const result = initiallyFolded(map);
    for (const [id, isFolded] of chosen) {
      if (isFolded) {
        result.add(id);
      } else {
        result.delete(id);
      }
    }
    return result;
  }, [map, chosen]);
  const toggle = (hostID: string) => setChosen((known) => new Map(known).set(hostID, !folded.has(hostID)));
  const hosts = topBoxes(map).filter(isHost);
  const foldAll = (fold: boolean) => setChosen(new Map(hosts.map((host) => [host.id, fold])));
  const allFolded = hosts.length > 0 && hosts.every((host) => folded.has(host.id));
  const context = {
    zones: new Map(map.zones.map((zone) => [zone.id, zone])),
    entries: new Map(map.entries.map((entry) => [entry.to, entry])),
    folded,
    toggle,
  };
  const home = splitIdle(map.home);
  const outside = splitIdle(map.outside);
  return (
    <div className="netmap-wrap">
      <div className="netmap-bar">
        <Legend map={map} />
        {hosts.length > 0 && (
          <Button small icon={allFolded ? "chevron-down" : "chevron-right"} onClick={() => foldAll(!allFolded)}>
            {t(allFolded ? "network.map_unfold_all" : "network.map_fold_all")}
          </Button>
        )}
      </div>
      <div className={`netmap${map.outside.length === 0 ? " no-outside" : ""}`} ref={setRoot} role="group" aria-label={t("network.map_label")}>
        {map.outside.length > 0 && (
          <section className="netmap-side netmap-outside" aria-label={t("network.map_outside")}>
            <h3 className="netmap-side-title">{t("network.map_outside")}</h3>
            {outside.busy.map((box) => (
              <MachineBox key={box.id} box={box} context={context} top />
            ))}
            <IdleLine boxes={outside.idle} />
          </section>
        )}
        <div className="netmap-internet" data-anchor={internetAnchorID}>
          <Icon name="globe" />
          <span className="netmap-internet-name">{t("network.internet")}</span>
          <span className="netmap-internet-count secondary">
            {map.entries.length === 0 ? t("network.internet_none") : t(map.entries.length === 1 ? "network.map_internet_entries_one" : "network.map_internet_entries", map.entries.length)}
          </span>
        </div>
        <section className="netmap-side netmap-home" data-anchor={homeAnchorID} aria-label={t("network.map_home")}>
          <h3 className="netmap-side-title">{t("network.map_home")}</h3>
          <div className="netmap-home-body">
            {map.firewalls.length > 0 && (
              <div className="netmap-firewalls">
                {map.firewalls.map((firewall) => (
                  <FirewallBox key={firewall.id} firewall={firewall} zones={context.zones} />
                ))}
              </div>
            )}
            <div className="netmap-machines">
              {home.busy.map((box) => (
                <MachineBox key={box.id} box={box} context={context} top />
              ))}
              <IdleLine boxes={home.idle.filter(isIdle)} />
            </div>
          </div>
        </section>
        <LinkLayer map={map} folded={folded} root={root} />
      </div>
      {map.links.length > 0 && <LinkList map={map} />}
    </div>
  );
}

// La légende ne dit que les traits que la carte porte.
function Legend({ map }: { map: MapData }) {
  const t = useT();
  const count = map.links.length;
  const proofs = new Set(map.links.map((link) => link.proof));
  return (
    <p className="netmap-legend secondary">
      {count > 0 && <span>{count === 1 ? t("link.overview_count_one", count) : t("link.overview_count", count)}</span>}
      {proofs.has("declared") && <span className="netmap-key netmap-key-declared">{t("network.map_legend_declared")}</span>}
      {proofs.has("deduced") && <span className="netmap-key netmap-key-deduced">{t("network.map_legend_deduced")}</span>}
      {proofs.has("observed") && <span className="netmap-key netmap-key-observed">{t("network.map_legend_observed")}</span>}
      {map.tunnels.length > 0 && <span className="netmap-key netmap-key-tunnel">{t("network.map_legend_tunnel")}</span>}
      {map.entries.length > 0 && <span className="netmap-key netmap-key-entry">{t("network.map_legend_entry")}</span>}
    </p>
  );
}
