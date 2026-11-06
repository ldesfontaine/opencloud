import { deleteBackupDestination, useBackups, type BackupDestination } from "../../api/backups";
import type { RightState } from "../../api/machines";
import { reasonText } from "../../components/ActionsCard";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { MissingRightButton } from "../../components/MissingRight";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { keyringPath, newDestinationPath } from "../../lib/backups";
import type { Tone } from "../../lib/tone";
import { formatWhen } from "../../lib/time";

const rightTones: Record<RightState, Tone> = { ok: "ok", missing: "danger", unavailable: "warn", unknown: "neutral" };

// Ce dont la base d'openCloud a besoin en plus d'un plan : la clé qui chiffre
// ses archives, et les machines qui les reçoivent. Ces deux réglages vivaient
// dans un bloc à part ; ils sont maintenant sur la ligne de la base, là où on
// regarde si elle est sauvegardée.
export function DatabaseSetup() {
  const t = useT();
  const now = useNow();
  const backups = useBackups();
  const action = useAction();
  if (backups.data === null) {
    return null;
  }
  const { keyring, destinations } = backups.data;
  const remove = (destination: BackupDestination) => {
    action.run(() => deleteBackupDestination(destination.id), { topic: "backups" });
  };
  return (
    <div className="backup-setup">
      <div className="backup-setup-line">
        <div className="backup-setup-say">
          <b>{t("backup.keyring_title")}</b>
          <span className="secondary">{keyring === null ? t("backup.keyring_none") : `${t("backup.fingerprint")} ${keyring.fingerprint} · ${formatWhen(keyring.created_at, now)}`}</span>
        </div>
        <Button small icon="shield" to={keyringPath}>
          {keyring === null ? t("backup.keyring_create") : t("backup.keyring_replace")}
        </Button>
      </div>
      <div className="backup-setup-line">
        <div className="backup-setup-say">
          <b>{t("backup.destination_title")}</b>
          {destinations.length === 0 && <span className="secondary">{t("backup.destination_none")}</span>}
        </div>
        <Button small icon="plus" to={newDestinationPath}>
          {t("backup.destination_add")}
        </Button>
      </div>
      {destinations.map((destination) => (
        <div key={destination.id} className="backup-setup-line backup-setup-sub">
          <div className="backup-setup-say">
            <span className="medium">{destination.name}</span>
            <span className="secondary">
              {destination.machine_name} · <span className="mono">{destination.path}</span>
            </span>
            {destination.right === "unavailable" && <span className="secondary">{reasonText(t, destination.right_reason ?? "")}</span>}
          </div>
          <span className="backup-setup-end">
            <Pill small tone={rightTones[destination.right]}>
              {t(`backup.right_pill_${destination.right}`)}
            </Pill>
            {destination.right === "missing" && <MissingRightButton machineID={destination.machine_id} right="backup.receive" path={destination.path} short />}
            <Button small variant="ghost" icon="x" disabled={action.busy} onClick={() => remove(destination)}>
              {t("backup.destination_delete")}
            </Button>
          </span>
        </div>
      ))}
      <Failure error={action.error} />
    </div>
  );
}
