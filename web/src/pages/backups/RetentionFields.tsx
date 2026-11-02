import type { BackupTarget } from "../../api/backups";
import { Field, Input } from "../../components/Field";
import { useT } from "../../i18n/context";
import { floorText } from "../../lib/backupOverview";

// Les nombres gardés que l'écran règle, du plus fin au plus large.
const retentionKeeps = ["keep_daily", "keep_weekly", "keep_monthly"] as const;

export type RetentionValues = Record<(typeof retentionKeeps)[number], string>;

// La rétention d'une sauvegarde restic, que la cible applique chaque nuit,
// et sous les champs le plancher de la cible : une rétention plus basse est
// refusée (par le serveur d'abord, par la cible sinon).
export function RetentionFields({ values, onChange, target }: { values: RetentionValues; onChange: (key: keyof RetentionValues, value: string) => void; target: Pick<BackupTarget, "name" | "retention_floor"> | undefined }) {
  const t = useT();
  const floor = target?.retention_floor == null ? null : floorText(t, target.retention_floor);
  return (
    <div className="stack gap-8">
      <div className="backup-row3">
        {retentionKeeps.map((key) => (
          <Field key={key} label={t(`backup.restic_${key}`)} htmlFor={`restic-${key}`}>
            <Input id={`restic-${key}`} mono type="number" inputMode="numeric" min={0} max={1000} value={values[key]} onChange={(event) => onChange(key, event.target.value)} required />
          </Field>
        ))}
      </div>
      <p className="secondary">
        {target === undefined ? t("backup.retention_applied_any") : t("backup.retention_applied", target.name)}
        {target !== undefined && floor !== null && ` ${t("backup.retention_floor_of", target.name, floor)}`}
      </p>
    </div>
  );
}
