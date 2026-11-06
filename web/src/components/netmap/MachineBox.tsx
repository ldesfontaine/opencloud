import { Link } from "react-router";

import type { MapBox, MapEntry, MapZone, NetworkMap } from "../../api/network";
import { useT } from "../../i18n/context";
import { badgeText, boxTarget, guestZones, isHost, isLit, serviceCount, shownServices, stateTone, worstState } from "../../lib/netmap";
import { Icon } from "../Icon";
import { Pill } from "../Pill";
import { ServiceChip } from "./ServiceChip";

// Ce que la carte d'une machine a besoin de savoir de la carte entière.
export interface BoxContext {
  map: NetworkMap;
  zones: ReadonlyMap<string, MapZone>;
  entries: ReadonlyMap<string, MapEntry>;
  boxes: ReadonlyMap<string, MapBox>;
  folded: ReadonlySet<string>;
  // La carte d'hôte en évidence (survolée ou choisie), avec ses invités.
  lit: string | null;
  pinned: string | null;
  // Le service qu'une entrée d'Internet désigne.
  pointed: string | null;
  hover: (hostID: string | null) => void;
  pin: (hostID: string) => void;
  revealHost: (hostID: string) => void;
  toggleFold: (hostID: string) => void;
}

// Une machine, en carte : son nom, son état, son type, son adresse, son hôte,
// ses services en puces. Un hôte dit combien de machines il porte ; le survol
// ou le clic met ses invités en évidence. À partir de quarante machines, un
// hôte sans défaut se replie sur son résumé.
export function MachineBox({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const host = isHost(box);
  const isFolded = host && context.folded.has(box.id);
  const target = boxTarget(box);
  const name = target === "" ? <span className="netmap-box-name">{box.name}</span> : <Link className="netmap-box-name" to={target}>{box.name}</Link>;
  const lit = isLit(box, context.lit);
  return (
    <article
      className={`netmap-box kind-${box.kind}${host ? " is-host" : ""}${box.managed ? "" : " is-unmanaged"}${isFolded ? " is-folded" : ""}${box.online || !box.managed ? "" : " is-offline"}${lit ? " is-lit" : ""}${context.lit !== null && !lit ? " is-dim" : ""}`}
      data-anchor={box.id}
      onPointerEnter={host ? () => context.hover(box.id) : undefined}
      onPointerLeave={host ? () => context.hover(null) : undefined}
      onFocus={host ? () => context.hover(box.id) : undefined}
      onBlur={host ? () => context.hover(null) : undefined}
    >
      <header className="netmap-box-head">
        <span className={`netmap-box-dot ${dotOf(box)}`} aria-hidden="true" />
        {name}
        <Pill tone={box.kind === "vps" ? "accent" : "neutral"} dot={false} small title={box.managed ? undefined : t("network.map_unmanaged")}>
          {badgeText(t, box)}
        </Pill>
      </header>
      <p className="netmap-box-meta">
        {box.address !== "" && <span className="netmap-box-address mono">{box.address}</span>}
        {box.host_id !== "" && <HostLink box={box} context={context} />}
      </p>
      {host && <HostLine box={box} context={context} />}
      {isFolded ? <FoldedSummary box={box} context={context} /> : <Chips box={box} context={context} />}
    </article>
  );
}

function dotOf(box: MapBox): string {
  if (!box.managed) {
    return box.guest_status === "running" ? "ok" : "neutral";
  }
  return box.online ? "ok" : "danger";
}

// « VM sur pve » : un clic amène à l'hôte et met ses invités en évidence.
function HostLink({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const hostName = context.boxes.get(box.host_id)?.name ?? "";
  const kindName = box.kind === "vm" || box.kind === "lxc" ? t(`machine.kind_${box.kind}`) : "";
  return (
    <button type="button" className="netmap-host-link" onClick={() => context.revealHost(box.host_id)}>
      {kindName === "" ? t("network.map_on_host_any", hostName) : t("network.map_on_host", kindName, hostName)}
    </button>
  );
}

// Le haut d'un hôte : combien de machines il héberge (un clic fige leur mise en
// évidence) et, quand la carte se replie, de quoi plier ou déplier.
function HostLine({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const isFolded = context.folded.has(box.id);
  return (
    <p className="netmap-host-line">
      <button type="button" className="netmap-hosting" aria-pressed={context.pinned === box.id} onClick={() => context.pin(box.id)}>
        <Icon name="server" />
        {t(box.guest_count === 1 ? "network.map_hosts_one" : "network.map_hosts", box.guest_count)}
      </button>
      {context.map.folding && (
        <button type="button" className="netmap-fold" aria-expanded={!isFolded} aria-label={t(isFolded ? "network.map_unfold" : "network.map_fold", box.name)} onClick={() => context.toggleFold(box.id)}>
          <Icon name={isFolded ? "chevron-right" : "chevron-down"} />
        </button>
      )}
    </p>
  );
}

// Un hôte replié dit ce qu'il cache : nombre de machines et de services, pire
// état, zones.
function FoldedSummary({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const services = serviceCount(context.map, box);
  const worst = worstState(context.map, box);
  return (
    <div className="netmap-folded">
      <span className="netmap-summary secondary">
        {t(services === 1 ? "network.map_services_one" : "network.map_services", services)}
        {worst !== null && (
          <Pill tone={stateTone(worst)} small>
            {t(`network.map_state_${worst}`)}
          </Pill>
        )}
      </span>
      <span className="netmap-folded-zones">
        {guestZones(context.map, box.id).map((zoneID) => {
          const zone = context.zones.get(zoneID);
          return (
            <span key={zoneID} className={`netmap-zone-tag${zone === undefined ? "" : ` zone-tone-${zone.tone}`}`}>
              {zone?.name ?? t("network.map_no_zone")}
            </span>
          );
        })}
      </span>
    </div>
  );
}

function Chips({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const { shown, hidden } = shownServices(box);
  if (shown.length === 0) {
    return <p className="netmap-no-service secondary">{t("network.map_no_service")}</p>;
  }
  return (
    <ul className="netmap-chips">
      {shown.map((service) => (
        <ServiceChip key={service.id} service={service} box={box} entry={context.entries.get(service.id)} pointed={context.pointed === service.id} />
      ))}
      {hidden > 0 && (
        <li className="netmap-chip-item">
          <Link className="netmap-chip-more" to={boxTarget(box) || "/services"}>
            {t("network.map_more", hidden)}
          </Link>
        </li>
      )}
    </ul>
  );
}
