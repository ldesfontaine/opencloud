import type { MapZone } from "../../api/network";
import { useT } from "../../i18n/context";
import { Icon } from "../Icon";

// Les zones du pare-feu où aucune machine ne tourne n'ont pas de couloir : une
// puce les compte, un clic les nomme.
export function EmptyZones({ zones, open, onToggle }: { zones: MapZone[]; open: boolean; onToggle: () => void }) {
  const t = useT();
  if (zones.length === 0) {
    return null;
  }
  return (
    <div className="netmap-empty-zones">
      <button
        type="button"
        className="netmap-empty-toggle"
        aria-expanded={open}
        onClick={onToggle}
      >
        <Icon name={open ? "chevron-down" : "chevron-right"} />
        {t(zones.length === 1 ? "network.map_empty_zones_one" : "network.map_empty_zones", zones.length)}
      </button>
      {open && (
        <ul className="netmap-empty-list">
          {zones.map((zone) => (
            <li key={zone.id} className={`netmap-empty-zone zone-tone-${zone.tone}`}>
              <span className="netmap-zone-name">{zone.name}</span>
              {zone.cidr !== "" && <span className="netmap-zone-meta mono">{zone.cidr}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
