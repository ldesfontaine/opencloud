import { Link } from "react-router";

import { useMachines, type Machine } from "../api/machines";
import { useI18n } from "../i18n/context";
import { kindFix } from "../lib/machineKind";
import { Pill } from "./Pill";
import "./MachineKind.scss";

// Où tourne la machine : matériel, VM, LXC, VPS. Un type qu'on ne sait pas
// reste neutre, jamais en défaut (ce n'est pas une panne), mais il ne se tait
// pas : il dit pourquoi en info-bulle et propose le geste qui le résout (mettre
// à jour l'agent, déclarer l'hôte Proxmox, corriger à la main).
// `withFix` à faux pour un endroit où le geste ne tient pas (un pied de tuile).
export function MachineKind({ machine, withFix = true }: { machine: Machine; withFix?: boolean }) {
  const { t, version } = useI18n();
  const all = useMachines().data?.machines ?? [];
  const fix = kindFix(machine, all, version);
  return (
    <span className="machine-kind">
      <Pill tone={machine.kind === "vps" ? "accent" : "neutral"} dot={false} small title={fix === null ? undefined : t(`machine.kind_why_${fix.action}`)}>
        {t(`machine.kind_${machine.kind}`)}
      </Pill>
      {withFix && fix !== null && (
        <Link className="kind-fix" to={fix.to}>
          {t(`machine.kind_fix_${fix.action}`)}
        </Link>
      )}
    </span>
  );
}
