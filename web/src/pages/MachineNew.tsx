import { useNavigate } from "react-router";

import { createMachineToken } from "../api/machines";
import { Checkbox, Checks, Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { alwaysGranted, capabilitiesOf, capabilityLabelKey, capabilityNames, emptyMachineForm, toggleRight } from "../lib/machines";

// L'opérateur nomme la machine et coche les droits de son agent ; le jeton
// naît et s'affiche une seule fois, sur la page suivante, avec la commande à
// coller, qui écrit ces droits dans le fichier de l'agent.
export function MachineNew() {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm(emptyMachineForm);
  const { values, change } = form;
  const action = useAction();

  const save = () => {
    action.run(() => createMachineToken(values.name, capabilitiesOf(values)), {
      topic: "all",
      onDone: (token) => void navigate("/machines/jeton", { state: token }),
    });
  };

  return (
    <>
      <PageHead title={t("machine.new_title")} subtitle={t("machine.new_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("machine.create_token")}
            submitIcon="plus"
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("machine.cancel")}
            cancelTo="/machines"
          />
        }
      >
        <Field label={t("machine.name_label")} htmlFor="machine-name" help={t("machine.name_help")}>
          <Input
            id="machine-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("machine.name_placeholder")}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("machine.rights_label")} help={t("machine.rights_help")}>
          <Checks>
            {capabilityNames.map((name) => (
              <Checkbox
                key={name}
                label={t(capabilityLabelKey(name))}
                checked={name === alwaysGranted || values.rights.includes(name)}
                disabled={name === alwaysGranted}
                onChange={() => change("rights", toggleRight(values.rights, name))}
              />
            ))}
          </Checks>
        </Field>
        {values.rights.includes("backup.receive") && (
          <Field label={t("machine.capability_backup_receive")} htmlFor="machine-backup-path" help={t("machine.rights_paths")}>
            <Input
              id="machine-backup-path"
              mono
              type="text"
              value={values.backupPath}
              onChange={(event) => change("backupPath", event.target.value)}
              placeholder={t("machine.backup_path_placeholder")}
              autoComplete="off"
              required
            />
          </Field>
        )}
        {values.rights.includes("files.write") && (
          <Field label={t("machine.capability_files_write")} htmlFor="machine-write-paths" help={t("machine.rights_paths")}>
            <Input
              id="machine-write-paths"
              mono
              type="text"
              value={values.writePaths}
              onChange={(event) => change("writePaths", event.target.value)}
              placeholder={t("machine.write_paths_placeholder")}
              autoComplete="off"
              required
            />
          </Field>
        )}
      </FormCard>
    </>
  );
}
