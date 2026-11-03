import { Link } from "react-router";

import type { MapBox, MapEntry, MapService } from "../../api/network";
import { useT } from "../../i18n/context";
import { chipTarget, portsText } from "../../lib/netmap";

// Un service dans sa machine : sa pastille d'état, son nom, et en toutes
// lettres tout état qui n'est pas « en marche » (la couleur ne dit jamais seule).
// Une entrée publique s'écrit sur la puce : les ports qu'Internet atteint.
export function ServiceChip({ service, box, entry }: { service: MapService; box: MapBox; entry: MapEntry | undefined }) {
  const t = useT();
  const state = t(`network.map_state_${service.state}`);
  const entryText = entry === undefined ? "" : t("network.map_entry", portsText(entry.ports));
  return (
    <li className="netmap-chip-item">
      <Link
        className={`netmap-chip state-${service.state} kind-${service.kind}`}
        to={chipTarget(service, box)}
        data-anchor={service.id}
        aria-label={`${service.label}, ${state}`}
        title={entry === undefined ? `${service.label} · ${state}` : `${service.label} · ${state} · ${entryText}`}
      >
        <span className="netmap-chip-dot" aria-hidden="true" />
        <span className="netmap-chip-name">{service.label}</span>
        {service.replicas > 1 && <span className="netmap-chip-extra mono">×{service.replicas}</span>}
        {service.state !== "running" && <span className="netmap-chip-state">{state}</span>}
        {entry !== undefined && entry.ports.length > 0 && <span className="netmap-chip-port mono">:{entry.ports[0]}</span>}
      </Link>
    </li>
  );
}
