import { useT } from "../i18n/context";
import { alwaysGranted, capabilityLabelKey, capabilityNames, toggleRight, type RightsValues } from "../lib/machines";
import { Checkbox, Checks, Field, Input } from "./Field";

// Les droits d'un agent à cocher, avec les chemins d'un droit borné : à
// l'ajout d'une machine comme au changement de ses droits. update écrit les
// seuls champs touchés : le formulaire de l'ajout porte aussi le nom.
export function RightsFields({ values, update }: { values: RightsValues; update: (patch: Partial<RightsValues>) => void }) {
  const t = useT();
  return (
    <>
      <Field label={t("machine.rights_label")} help={t("machine.rights_help")}>
        <Checks>
          {capabilityNames.map((name) => (
            <Checkbox
              key={name}
              label={t(capabilityLabelKey(name))}
              checked={name === alwaysGranted || values.rights.includes(name)}
              disabled={name === alwaysGranted}
              onChange={() => update({ rights: toggleRight(values.rights, name) })}
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
            onChange={(event) => update({ backupPath: event.target.value })}
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
            onChange={(event) => update({ writePaths: event.target.value })}
            placeholder={t("machine.write_paths_placeholder")}
            autoComplete="off"
            required
          />
        </Field>
      )}
    </>
  );
}
