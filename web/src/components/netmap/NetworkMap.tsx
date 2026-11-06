import { useMemo, useState } from "react";

import { type NetworkMap as MapData, useNetworkMap } from "../../api/network";
import { useT } from "../../i18n/context";
import { allBoxes, foldedHosts, homeAnchorID, isHidden, isHost } from "../../lib/netmap";
import { Button } from "../Button";
import { EmptyCard } from "../Card";
import { Failure } from "../Failure";
import { Icon } from "../Icon";
import { EmptyZones } from "./EmptyZones";
import { FirewallBox } from "./FirewallBox";
import { InternetRegion } from "./InternetRegion";
import { LinkLayer } from "./LinkLayer";
import { LinkList } from "./LinkList";
import { type BoxContext, MachineBox } from "./MachineBox";
import { ZoneLane } from "./ZoneLane";
import "./NetworkMap.scss";

// La carte du réseau de la vue d'ensemble, en trois régions de gauche à droite :
// Internet (les entrées publiques), Ailleurs (les machines hors de la maison),
// Chez vous (le pare-feu en bandeau, puis un couloir par zone). Le serveur
// décide où va chaque chose ; ici on dessine. Les cartes sont des boîtes HTML
// qui se placent seules ; un calque SVG trace par-dessus ce que les données
// relient (entrées, liens, tunnels).
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
  // Ce que l'opérateur a replié ou déplié à la main l'emporte sur le réglage du
  // serveur, et un rafraîchissement n'y touche pas.
  const [chosen, setChosen] = useState<ReadonlyMap<string, boolean>>(new Map());
  const [hovered, setHovered] = useState<string | null>(null);
  const [pinned, setPinned] = useState<string | null>(null);
  const [pointed, setPointed] = useState<string | null>(null);
  const [emptyOpen, setEmptyOpen] = useState(false);
  const folded = useMemo(() => foldedHosts(map, chosen), [map, chosen]);
  const hosts = allBoxes(map).filter(isHost);
  const allFolded = hosts.length > 0 && hosts.every((host) => folded.has(host.id));
  const scrollTo = (anchor: string) => {
    const card = [...(root?.querySelectorAll<HTMLElement>("[data-anchor]") ?? [])].find((found) => found.dataset.anchor === anchor);
    card?.scrollIntoView({ block: "center" });
  };
  const revealHost = (hostID: string) => {
    setPinned(hostID);
    scrollTo(hostID);
  };
  // Une entrée d'Internet met son service en évidence ; le trait dit où il est.
  const pointService = (serviceID: string) => setPointed((known) => (known === serviceID ? null : serviceID));
  const context: BoxContext = {
    map,
    zones: new Map(map.zones.map((zone) => [zone.id, zone])),
    entries: new Map(map.entries.map((entry) => [entry.to, entry])),
    boxes: new Map(allBoxes(map).map((box) => [box.id, box])),
    folded,
    lit: hovered ?? pinned,
    pinned,
    pointed,
    hover: setHovered,
    pin: (hostID) => setPinned((known) => (known === hostID ? null : hostID)),
    revealHost,
    toggleFold: (hostID) => setChosen((known) => new Map(known).set(hostID, !folded.has(hostID))),
  };
  const emptyZones = map.empty_zones.flatMap((id) => context.zones.get(id) ?? []);
  return (
    <div className="netmap-wrap">
      <div className="netmap-bar">
        <Legend map={map} />
        {map.folding && hosts.length > 0 && (
          <Button small icon={allFolded ? "chevron-down" : "chevron-right"} onClick={() => setChosen(new Map(hosts.map((host) => [host.id, !allFolded])))}>
            {t(allFolded ? "network.map_unfold_all" : "network.map_fold_all")}
          </Button>
        )}
      </div>
      <div className={`netmap${map.outside.length === 0 ? " no-outside" : ""}`} ref={setRoot} role="group" aria-label={t("network.map_label")}>
        <InternetRegion map={map} pointed={pointed} onPoint={pointService} />
        {map.outside.length > 0 && (
          <section className="netmap-region netmap-outside" aria-label={t("network.map_outside")}>
            <h3 className="netmap-region-title">
              <Icon name="server" />
              {t("network.map_outside")}
            </h3>
            <p className="netmap-region-hint secondary">{t("network.map_outside_hint")}</p>
            {map.outside
              .filter((box) => !isHidden(box, folded))
              .map((box) => (
                <MachineBox key={box.id} box={box} context={context} />
              ))}
          </section>
        )}
        <section className="netmap-region netmap-home" data-anchor={homeAnchorID} aria-label={t("network.map_home")}>
          <h3 className="netmap-region-title">
            <Icon name="network" />
            {t("network.map_home")}
          </h3>
          {map.firewalls.map((firewall) => (
            <FirewallBox key={firewall.id} firewall={firewall} />
          ))}
          <div className="netmap-lanes">
            {map.home.map((lane) => {
              const visible = lane.boxes.filter((box) => !isHidden(box, folded));
              return visible.length === 0 ? null : (
                <ZoneLane key={lane.zone_id} zone={context.zones.get(lane.zone_id)}>
                  {visible.map((box) => (
                    <MachineBox key={box.id} box={box} context={context} />
                  ))}
                </ZoneLane>
              );
            })}
          </div>
          <EmptyZones zones={emptyZones} open={emptyOpen} onToggle={() => setEmptyOpen(!emptyOpen)} />
        </section>
        <LinkLayer map={map} folded={folded} root={root} watch={[emptyOpen]} />
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
