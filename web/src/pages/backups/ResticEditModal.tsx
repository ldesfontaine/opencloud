import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { updateResticBackup, useBackupOverview, type BackupProtection, type BackupSource, type BackupTarget } from "../../api/backups";
import { Field, Input } from "../../components/Field";
import { ModalForm } from "../../components/ModalForm";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { backupsPath } from "../../lib/backups";
import { isFormModified } from "../../lib/modal";
import { RetentionFields } from "./RetentionFields";

// « Modifier » une sauvegarde restic : la fenêtre de la page Sauvegardes, à
// /sauvegardes/restic/:machineId/:backupId/modifier. Le nom et la rétention,
// que la cible applique chaque nuit ; la source, la cible et ce qui est
// sauvegardé ne changent pas (ce serait un autre dépôt).
export function ResticEditModal() {
  const { machineId, backupId } = useParams();
  const overview = useBackupOverview();
  if (overview.data === null) {
    return null;
  }
  for (const source of overview.data.sources) {
    const protection = source.protections.find((candidate) => candidate.origin === "restic" && candidate.id === backupId && source.machine_id === machineId);
    if (protection !== undefined) {
      const target = overview.data.targets.find((candidate) => candidate.kind === "restic" && candidate.machine_id === protection.target_machine_id);
      return <ResticEditDialog source={source} protection={protection} target={target} />;
    }
  }
  return <Navigate to={backupsPath} replace />;
}

function ResticEditDialog({ source, protection, target }: { source: BackupSource; protection: BackupProtection; target: BackupTarget | undefined }) {
  const t = useT();
  const navigate = useNavigate();
  const retention = protection.retention;
  const [initial] = useState(() => ({
    name: protection.name,
    keep_daily: String(retention.keep_daily),
    keep_weekly: String(retention.keep_weekly),
    keep_monthly: String(retention.keep_monthly),
  }));
  const form = useForm(initial);
  const { values, change } = form;
  const action = useAction();

  const close = () => void navigate(backupsPath, { replace: true });
  const save = () => {
    const request = {
      name: values.name.trim(),
      retention: { keep_last: retention.keep_last, keep_daily: Number(values.keep_daily), keep_weekly: Number(values.keep_weekly), keep_monthly: Number(values.keep_monthly), keep_yearly: retention.keep_yearly },
    };
    action.run(() => updateResticBackup(source.machine_id, protection.id, request), { topic: "backups", onDone: close });
  };

  return (
    <ModalForm
      title={t("backup.restic_edit_title", source.name)}
      onClose={close}
      dirty={isFormModified(values, initial)}
      onSubmit={save}
      submitLabel={t("backup.plan_save")}
      submitIcon="check"
      busy={action.busy}
      error={action.error}
    >
      <Field label={t("backup.restic_name")} htmlFor="restic-name" help={t("backup.restic_name_help")}>
        <Input id="restic-name" type="text" autoComplete="off" maxLength={80} value={values.name} onChange={(event) => change("name", event.target.value)} required />
      </Field>
      <RetentionFields values={values} onChange={change} target={target ?? { name: protection.target_machine_name, retention_floor: null }} />
    </ModalForm>
  );
}
