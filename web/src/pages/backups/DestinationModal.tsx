import { useNavigate } from "react-router";

import { createBackupDestination } from "../../api/backups";
import { useMachines } from "../../api/machines";
import { Field, Input } from "../../components/Field";
import { MachineSelect } from "../../components/MachineSelect";
import { ModalForm } from "../../components/ModalForm";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { backupsPath } from "../../lib/backups";

// « Ajouter la destination » : la fenêtre de la page Sauvegardes, à
// /sauvegardes/destinations/nouvelle. Une machine dont l'agent reçoit les
// sauvegardes, et le dossier où il les range.
export function DestinationModal() {
  const t = useT();
  const navigate = useNavigate();
  const machines = useMachines();
  const form = useForm({ name: "", machine: "", path: "" });
  const { values, change } = form;
  const action = useAction();

  const close = () => void navigate(backupsPath, { replace: true });
  const add = () => {
    action.run(() => createBackupDestination({ name: values.name, machine_id: values.machine, path: values.path }), { topic: "backups", onDone: close });
  };

  return (
    <ModalForm
      title={t("backup.destination_add")}
      onClose={close}
      dirty={values.name !== "" || values.machine !== "" || values.path !== ""}
      onSubmit={add}
      submitLabel={t("backup.destination_add")}
      submitIcon="plus"
      busy={action.busy}
      canSubmit={values.machine !== ""}
      error={action.error}
    >
      <Field label={t("backup.destination_name")} htmlFor="backup-destination-name">
        <Input id="backup-destination-name" type="text" autoComplete="off" maxLength={63} value={values.name} onChange={(event) => change("name", event.target.value)} autoFocus required />
      </Field>
      <Field label={t("backup.destination_machine")} htmlFor="backup-destination-machine">
        <MachineSelect id="backup-destination-machine" machines={machines.data?.machines ?? []} value={values.machine} onChange={(id) => change("machine", id)} noneLabel={t("backup.destination_machine_choose")} />
      </Field>
      <Field label={t("backup.destination_path")} htmlFor="backup-destination-path" help={t("backup.destination_path_help")}>
        <Input id="backup-destination-path" mono type="text" autoComplete="off" value={values.path} onChange={(event) => change("path", event.target.value)} required />
      </Field>
    </ModalForm>
  );
}
