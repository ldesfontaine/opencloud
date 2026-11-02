import { Link } from "react-router";

import type { MapBox, MapEntry, MapGroup, MapZone } from "../../api/network";
import { useT } from "../../i18n/context";
import { badgeText, boxTarget, guestsOf, isHost, serviceCount, shownServices, splitIdle, stateTone, worstState, zoneAnchorID } from "../../lib/netmap";
import { Icon } from "../Icon";
import { Pill } from "../Pill";
import { ServiceChip } from "./ServiceChip";

// Ce que la boîte d'une machine a besoin de savoir de la carte entière.
interface BoxContext {
  zones: ReadonlyMap<string, MapZone>;
  entries: ReadonlyMap<string, MapEntry>;
  folded: ReadonlySet<string>;
  toggle: (hostID: string) => void;
}

// Une machine : son nom, son type, ses services en puces ; un hôte porte en
// plus ses invités, rangés par zone. Un hôte se replie sur son en-tête.
export function MachineBox({ box, context, top }: { box: MapBox; context: BoxContext; top: boolean }) {
  const t = useT();
  const host = isHost(box);
  const isFolded = host && context.folded.has(box.id);
  const zone = context.zones.get(box.zone_id);
  const target = boxTarget(box);
  const name = target === "" ? <span className="netmap-box-name">{box.name}</span> : <Link className="netmap-box-name" to={target}>{box.name}</Link>;
  return (
    <article
      className={`netmap-box kind-${box.kind}${host ? " is-host" : ""}${box.managed ? "" : " is-unmanaged"}${isFolded ? " is-folded" : ""}${box.online || !box.managed ? "" : " is-offline"}${top && zone !== undefined ? ` zone-tone-${zone.tone}` : ""}`}
      data-anchor={box.id}
    >
      <header className="netmap-box-head">
        {host && (
          <button type="button" className="netmap-fold" aria-expanded={!isFolded} aria-label={t(isFolded ? "network.map_unfold" : "network.map_fold", box.name)} onClick={() => context.toggle(box.id)}>
            <Icon name={isFolded ? "chevron-right" : "chevron-down"} />
          </button>
        )}
        <span className={`netmap-box-dot ${dotOf(box)}`} aria-hidden="true" />
        {name}
        <Pill tone={box.kind === "vps" ? "accent" : "neutral"} dot={false} small title={box.managed ? undefined : t("network.map_unmanaged")}>
          {badgeText(t, box)}
        </Pill>
        {box.address !== "" && <span className="netmap-box-address mono">{box.address}</span>}
        {top && !host && zone !== undefined && <span className="netmap-zone-tag">{zone.name}</span>}
        {host && <HostSummary box={box} />}
        {isFolded && box.groups.map((group) => <FoldedZone key={group.zone_id} zone={context.zones.get(group.zone_id)} />)}
      </header>
      {!isFolded && <Chips box={box} context={context} />}
      {host && !isFolded && (
        <div className="netmap-zones">
          {box.groups.map((group) => (
            <ZoneGroup key={group.zone_id} host={box} group={group} context={context} />
          ))}
        </div>
      )}
    </article>
  );
}

// Un hôte replié dit encore ses zones : leur nom, à leur teinte.
function FoldedZone({ zone }: { zone: MapZone | undefined }) {
  const t = useT();
  return <span className={`netmap-zone-tag${zone === undefined ? "" : ` zone-tone-${zone.tone}`}`}>{zone?.name ?? t("network.map_no_zone")}</span>;
}

function dotOf(box: MapBox): string {
  if (!box.managed) {
    return box.guest_status === "running" ? "ok" : "neutral";
  }
  return box.online ? "ok" : "danger";
}

function HostSummary({ box }: { box: MapBox }) {
  const t = useT();
  const machines = guestsOf(box).length;
  const services = serviceCount(box);
  const worst = worstState(box);
  return (
    <span className="netmap-summary secondary">
      {t(machines === 1 ? "network.map_machines_one" : "network.map_machines", machines)} · {t(services === 1 ? "network.map_services_one" : "network.map_services", services)}
      {worst !== null && (
        <Pill tone={stateTone(worst)} small>
          {t(`network.map_state_${worst}`)}
        </Pill>
      )}
    </span>
  );
}

function Chips({ box, context }: { box: MapBox; context: BoxContext }) {
  const t = useT();
  const { shown, hidden } = shownServices(box);
  if (shown.length === 0) {
    return null;
  }
  return (
    <ul className="netmap-chips">
      {shown.map((service) => (
        <ServiceChip key={service.id} service={service} box={box} entry={context.entries.get(service.id)} />
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

// Les invités d'une même zone dans un hôte : l'en-tête dit la zone (son nom,
// son sous-réseau, son tag VLAN), la teinte la rend reconnaissable d'un coup
// d'œil. Les machines sans service ne sont pas des boîtes : une ligne les dit.
function ZoneGroup({ host, group, context }: { host: MapBox; group: MapGroup; context: BoxContext }) {
  const t = useT();
  const zone = context.zones.get(group.zone_id);
  const { busy, idle } = splitIdle(group.guests);
  return (
    <section className={`netmap-zone${zone === undefined ? "" : ` zone-tone-${zone.tone}`}`} data-anchor={zoneAnchorID(host.id, group.zone_id)}>
      <header className="netmap-zone-head">
        <span className="netmap-zone-name">{zone?.name ?? t("network.map_no_zone")}</span>
        {zone !== undefined && zone.vlan > 0 && zone.source !== "vlan" && <span className="netmap-zone-meta mono">{t("network.map_vlan", zone.vlan)}</span>}
        {zone !== undefined && zone.cidr !== "" && <span className="netmap-zone-meta mono">{zone.cidr}</span>}
      </header>
      {busy.length > 0 && (
        <div className="netmap-guests">
          {busy.map((guest) => (
            <MachineBox key={guest.id} box={guest} context={context} top={false} />
          ))}
        </div>
      )}
      <IdleLine boxes={idle} />
    </section>
  );
}

// Les machines sans service, repliées en une ligne : leur nom mène à leur fiche.
export function IdleLine({ boxes }: { boxes: MapBox[] }) {
  const t = useT();
  if (boxes.length === 0) {
    return null;
  }
  return (
    <p className="netmap-idle secondary">
      <span>{t(boxes.length === 1 ? "network.map_idle_one" : "network.map_idle", boxes.length)}</span>{" "}
      {boxes.map((box, index) => (
        <span key={box.id}>
          {index > 0 && " · "}
          {boxTarget(box) === "" ? (
            <span title={t("network.map_unmanaged")}>{box.name}</span>
          ) : (
            <Link to={boxTarget(box)}>{box.name}</Link>
          )}
          {box.vmid > 0 && <span className="mono"> {box.vmid}</span>}
        </span>
      ))}
    </p>
  );
}
