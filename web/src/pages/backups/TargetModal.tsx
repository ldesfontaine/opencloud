import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { errorKey } from "../../api/client";
import { setupResticTarget, useBackupOverview, type BackupOverview } from "../../api/backups";
import { useMachines, type Machine } from "../../api/machines";
import { Note } from "../../components/Card";
import { Field, Input } from "../../components/Field";
import { Icon } from "../../components/Icon";
import { MachineSelect } from "../../components/MachineSelect";
import { ModalForm } from "../../components/ModalForm";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { defaultFloorDays, rightState, targetSetupUnfit } from "../../lib/backupOverview";
import { backupsPath } from "../../lib/backups";
import { isFormModified } from "../../lib/modal";
import { missingSoftware } from "../../lib/resticInstall";
import { InstallBlock } from "./InstallBlock";

// Le port de rest-server quand l'opérateur n'en dit pas d'autre.
const defaultPort = "8000";

// « Faire de cette machine une cible » : la fenêtre de la page Sauvegardes, à
// /sauvegardes/cibles/nouvelle (?machine= choisit d'avance). L'adresse que les
// sources joignent, le port, le dossier des dépôts (celui du droit) et le
// plancher de rétention. L'agent de la machine lance rest-server en ajout
// seul (il ne le télécharge pas : sinon, la fenêtre dit quoi installer) ;
// son état revient par le signal. La fenêtre attend l'aperçu (les droits) et
// la liste des machines (leur type, leur adresse).
export function TargetModal() {
  const overview = useBackupOverview();
  const machines = useMachines();
  if (overview.data === null || machines.data === null) {
    return null;
  }
  return <TargetDialog overview={overview.data} machines={machines.data.machines} />;
}

function TargetDialog({ overview, machines }: { overview: BackupOverview; machines: Machine[] }) {
  const t = useT();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const fitOf = (machine: Pick<Machine, "id">) => targetSetupUnfit(t, overview.machines.find((candidate) => candidate.machine_id === machine.id), machine.id);
  const [initial] = useState(() => {
    const wanted = machines.find((machine) => machine.id === search.get("machine") && fitOf(machine) === null);
    const chosen = wanted ?? machines.find((machine) => fitOf(machine) === null);
    return formFor(overview, chosen, { machine: "", host: "", port: defaultPort, dir: "", floor: String(defaultFloorDays) });
  });
  const form = useForm(initial);
  const { values, change, update } = form;
  const action = useAction();
  const chosen = machines.find((machine) => machine.id === values.machine);
  // Les machines qui auraient pu être cibles, sans rest-server : ce qu'il faut
  // y installer. Un refus du serveur pour la même raison mène au même bloc.
  const refusedMissing = action.error !== null && missingSoftware(errorKey(action.error)) === "rest-server";
  const lacking = overview.machines.filter(
    (machine) => (refusedMissing && machine.machine_id === values.machine) || (!machine.is_target && rightState(machine, "restic.target").reason === "rest_server_missing"),
  );

  const close = () => void navigate(backupsPath, { replace: true });
  const choose = (id: string) => update(formFor(overview, machines.find((machine) => machine.id === id), values));
  const save = () => {
    const floor = { keep_daily: Number(values.floor), keep_weekly: 0, keep_monthly: 0 };
    action.run(() => setupResticTarget(values.machine, { host: values.host.trim(), port: Number(values.port), dir: values.dir.trim(), retention_floor: floor }), { topic: "backups", onDone: close });
  };

  return (
    <ModalForm
      title={t("backup.target_setup_title")}
      onClose={close}
      dirty={isFormModified(values, initial)}
      onSubmit={save}
      submitLabel={chosen === undefined ? t("backup.make_target") : t("backup.target_setup_submit", chosen.name)}
      submitIcon="server"
      busy={action.busy}
      canSubmit={values.machine !== ""}
      error={action.error}
      errorGesture={values.machine === "" ? undefined : { machineID: values.machine, path: values.dir }}
    >
      <Field label={t("backup.target_setup_machine")} htmlFor="restic-target-machine" help={t("backup.target_setup_machine_help")}>
        <MachineSelect id="restic-target-machine" machines={machines} fit={fitOf} value={values.machine} onChange={choose} noneLabel={t("backup.destination_machine_choose")} />
      </Field>
      <div className="backup-row2">
        <Field label={t("backup.target_setup_host")} htmlFor="restic-target-host" help={t("backup.target_setup_host_help")}>
          <Input id="restic-target-host" mono type="text" autoComplete="off" value={values.host} onChange={(event) => change("host", event.target.value)} required />
        </Field>
        <Field label={t("backup.target_setup_port")} htmlFor="restic-target-port">
          <Input id="restic-target-port" mono type="number" inputMode="numeric" min={1} max={65535} value={values.port} onChange={(event) => change("port", event.target.value)} required />
        </Field>
      </div>
      <div className="backup-row2">
        <Field label={t("backup.target_setup_dir")} htmlFor="restic-target-dir" help={t("backup.target_setup_dir_help")}>
          <Input id="restic-target-dir" mono type="text" autoComplete="off" value={values.dir} onChange={(event) => change("dir", event.target.value)} />
        </Field>
        <Field label={t("backup.target_setup_floor")} htmlFor="restic-target-floor" help={t("backup.target_setup_floor_help")}>
          <Input id="restic-target-floor" mono type="number" inputMode="numeric" min={0} max={1000} value={values.floor} onChange={(event) => change("floor", event.target.value)} required />
        </Field>
      </div>
      {lacking.map((machine) => (
        <InstallBlock key={machine.machine_id} software="rest-server" machine={machine} framed />
      ))}
      <Note tone="neutral">
        <span className="backup-note-line">
          <Icon name="archive" />
          {t("backup.target_setup_purge", chosen?.name ?? t("backup.target_setup_this"))}
        </span>
      </Note>
      <Note tone="neutral">
        <span className="backup-note-line">
          <Icon name="lock" />
          {t("backup.target_setup_note")}
        </span>
      </Note>
    </ModalForm>
  );
}

interface TargetForm {
  machine: string;
  host: string;
  port: string;
  dir: string;
  floor: string;
}

// Choisir une machine propose son adresse et le dossier de son droit ; le
// port saisi reste.
function formFor(overview: BackupOverview, machine: Machine | undefined, current: TargetForm): TargetForm {
  if (machine === undefined) {
    return { ...current, machine: "" };
  }
  const right = rightState(
    overview.machines.find((candidate) => candidate.machine_id === machine.id),
    "restic.target",
  );
  return { machine: machine.id, host: machine.address, port: current.port, dir: right.path, floor: current.floor };
}
