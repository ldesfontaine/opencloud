import { useBackups } from "../api/backups";
import { useT } from "../i18n/context";
import { anchorHelpKey, anchorKindsFor, anchorLabelKey, withKeyringRecipient } from "../lib/anchors";
import { alwaysGranted, capabilityLabelKey, offeredCapabilities, toggleRight, type RightsValues } from "../lib/machines";
import "./AgentAnchors.scss";
import { Note } from "./Card";
import { Checkbox, Checks, Field, Input } from "./Field";

// Les droits d'un agent à cocher, avec les chemins d'un droit borné : à
// l'ajout d'une machine comme au changement de ses droits. update écrit les
// seuls champs touchés : le formulaire de l'ajout porte aussi le nom.
// proxmoxDetected : l'agent a reconnu un hôte Proxmox VE sur la machine ; faux
// pour une machine pas encore enrôlée.
export function RightsFields({
  values,
  update,
  proxmoxDetected,
}: {
  values: RightsValues;
  update: (patch: Partial<RightsValues>) => void;
  proxmoxDetected: boolean;
}) {
  const t = useT();
  // La clé du trousseau de sauvegarde : cochée, backup.produce la propose comme
  // ancre de chiffrement, que l'opérateur peut corriger.
  const recipient = useBackups().data?.keyring?.recipient;
  const toggle = (name: string) => {
    const rights = toggleRight(values.rights, name);
    update({ rights, anchors: rights.includes("backup.produce") ? withKeyringRecipient(values.anchors, recipient) : values.anchors });
  };
  const anchorKinds = anchorKindsFor(values.rights);
  return (
    <>
      <Field label={t("machine.rights_label")} help={t("machine.rights_help")} tip={t("machine.rights_tip")}>
        <Checks>
          {offeredCapabilities(proxmoxDetected, values.rights).map((name) => (
            <Checkbox
              key={name}
              label={t(capabilityLabelKey(name))}
              checked={name === alwaysGranted || values.rights.includes(name)}
              disabled={name === alwaysGranted}
              onChange={() => toggle(name)}
            />
          ))}
        </Checks>
      </Field>
      {values.rights.includes("observe.env") && <Note tone="neutral">{t("machine.capability_observe_env_note")}</Note>}
      {values.rights.includes("restic.docker") && <Note tone="neutral">{t("machine.capability_restic_docker_note")}</Note>}
      {values.rights.includes("restic.verify") && <Note tone="neutral">{t("machine.capability_restic_verify_note")}</Note>}
      {values.rights.includes("restic.browse") && <Note tone="neutral">{t("machine.capability_restic_browse_note")}</Note>}
      {values.rights.includes("restic.restore") && <Note tone="neutral">{t("machine.capability_restic_restore_note")}</Note>}
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
      {values.rights.includes("restic.target") && (
        <Field label={t("machine.capability_restic_target")} htmlFor="machine-restic-path" help={t("machine.restic_path_help")}>
          <Input
            id="machine-restic-path"
            mono
            type="text"
            value={values.resticPath}
            onChange={(event) => update({ resticPath: event.target.value })}
            placeholder={t("machine.restic_path_placeholder")}
            autoComplete="off"
            required
          />
        </Field>
      )}
      {anchorKinds.length > 0 && (
        <fieldset className="anchor-fields">
          <legend>{t("anchor.form_title")}</legend>
          <p className="secondary">{t("anchor.form_help")}</p>
          {anchorKinds.map((kind) => (
            <Field key={kind.name} label={t(anchorLabelKey(kind.name))} htmlFor={`anchor-${kind.name}`} help={`${t(anchorHelpKey(kind.name))} ${t("anchor.form_values_help")}`}>
              <Input
                id={`anchor-${kind.name}`}
                mono
                type="text"
                value={values.anchors[kind.name] ?? ""}
                onChange={(event) => update({ anchors: { ...values.anchors, [kind.name]: event.target.value } })}
                autoComplete="off"
              />
            </Field>
          ))}
        </fieldset>
      )}
    </>
  );
}
