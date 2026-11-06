import type { ServiceUpdate } from "../api/directory";
import { useT } from "../i18n/context";
import { failedUpdate } from "../lib/updates";
import { formatClock } from "../lib/time";
import { Pill } from "./Pill";
import "./ServiceUpdateNote.scss";

// La dernière montée de version d'un service, quand elle demande un regard :
// encore jugée par la machine, ou revenue à la version d'avant. Une montée
// qui tient, ou aucune, ne dit rien. Partagée par la carte Mise à jour d'un
// conteneur et la carte Déployer du service.
export function ServiceUpdateNote({ update }: { update: ServiceUpdate | null }) {
  const t = useT();
  if (update === null) {
    return null;
  }
  if (update.state === "pending") {
    return <span className="secondary">{t("update.pending_note", update.to_version)}</span>;
  }
  const failure = failedUpdate(update);
  if (failure === null) {
    return null;
  }
  return (
    <div className="service-update-note">
      <Pill tone={failure.tone}>{t("update.failed_title", update.to_version)}</Pill>
      <span className="secondary">{t(failure.key, formatClock(update.settled_at ?? update.requested_at), update.from_version)}</span>
    </div>
  );
}
