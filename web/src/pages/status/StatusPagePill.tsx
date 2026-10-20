import type { StatusState } from "../../api/status";
import { Pill } from "../../components/Pill";
import { useT } from "../../i18n/context";
import { statusStateTones } from "../../lib/status";

// La pastille d'un état de la page de statut ; vide : rien à dire.
export function StatusPagePill({ state }: { state: StatusState }) {
  const t = useT();
  return <Pill tone={statusStateTones[state]}>{state === "" ? "—" : t(`status.state_${state}`)}</Pill>;
}
