import type { MapEntry, NetworkMap } from "../../api/network";
import { useT } from "../../i18n/context";
import { entryAnchorID, portsText, type ServiceSpot, serviceIndex } from "../../lib/netmap";
import { Icon } from "../Icon";
import { Pill } from "../Pill";

// Internet : une carte par entrée publique, reliée par un trait à la carte du
// service qui la sert. Le trait se tire dans LinkLayer, d'après l'ancre ; un
// clic sur la carte amène au service et le met en évidence.
export function InternetRegion({ map, pointed, onPoint }: { map: NetworkMap; pointed: string | null; onPoint: (serviceID: string) => void }) {
  const t = useT();
  const spots = serviceIndex(map);
  return (
    <section className="netmap-region netmap-internet" aria-label={t("network.internet")}>
      <h3 className="netmap-region-title">
        <Icon name="globe" />
        {t("network.internet")}
      </h3>
      <p className="netmap-region-hint secondary">{t("network.map_internet_hint")}</p>
      {map.entries.length === 0 ? (
        <p className="netmap-internet-none secondary">{t("network.internet_none")}</p>
      ) : (
        <ul className="netmap-entries">
          {map.entries.map((entry) => (
            <EntryCard key={entry.to} entry={entry} spot={spots.get(entry.to)} pressed={pointed === entry.to} onPoint={onPoint} />
          ))}
        </ul>
      )}
    </section>
  );
}

function EntryCard({ entry, spot, pressed, onPoint }: { entry: MapEntry; spot: ServiceSpot | undefined; pressed: boolean; onPoint: (serviceID: string) => void }) {
  const t = useT();
  const label = spot?.service.label ?? entry.to;
  return (
    <li className={`netmap-entry${entry.observed ? " is-observed" : ""}`} data-anchor={entryAnchorID(entry.to)}>
      <button type="button" aria-pressed={pressed} onClick={() => onPoint(entry.to)}>
        <span className="netmap-entry-name">{label}</span>
        {spot !== undefined && <span className="netmap-entry-where secondary">{t("network.map_entry_on", spot.box.name)}</span>}
        {entry.ports.length > 0 && <span className="netmap-entry-ports mono">{t("network.map_entry_ports", portsText(entry.ports))}</span>}
        {entry.observed && (
          <Pill tone="accent" dot={false} small title={t("network.map_entry_seen")}>
            {t("network.map_entry_seen_short")}
          </Pill>
        )}
      </button>
    </li>
  );
}
