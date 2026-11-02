import { useState } from "react";

import { restoreResticBackup, type BackupProtection, type BackupSource } from "../../api/backups";
import { useMachineActions, type ActionRun } from "../../api/actions";
import { reasonText } from "../../components/ActionsCard";
import { ConfirmInline } from "../../components/ConfirmInline";
import { Failure } from "../../components/Failure";
import type { FileSelection } from "../../components/FileExplorer";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { restoreDestination, restoreSourcePath, restoreStamp } from "../../lib/restore";
import type { Tone } from "../../lib/tone";

const stateTones: Record<string, Tone> = { sent: "neutral", accepted: "accent", succeeded: "ok", failed: "danger", refused: "danger", expired: "warn" };

// La sortie « Restaurer » de l'explorateur : le fichier ou le dossier choisi
// revient À CÔTÉ de l'original, sur la machine source, sous <chemin>.restaure-
// <horodatage>. La confirmation dit ce chemin exact ; rien n'est écrasé ni
// supprimé, et une destination qui existe est refusée. L'ordre part à la
// source ; son résultat se lit ici, à mesure qu'elle le rapporte.
export function RestoreOutput({ source, protection, selection }: { source: BackupSource; protection: BackupProtection; selection: FileSelection }) {
  const t = useT();
  const action = useAction();
  const [stamp, setStamp] = useState(() => restoreStamp(new Date()));
  const [started, setStarted] = useState<{ id: string; destination: string }[]>([]);
  const path = restoreSourcePath(selection.folder, selection.names);
  const snapshot = selection.view?.id ?? "";
  if (selection.view === null) {
    return <span className="secondary">{t("backup.explorer_pick_snapshot")}</span>;
  }
  if (path === null) {
    return <span className="secondary">{t(selection.names.length > 1 ? "backup.restore_one_at_a_time" : "backup.restore_pick")}</span>;
  }
  const destination = restoreDestination(path, stamp);
  const restore = () => {
    action.run(() => restoreResticBackup(source.machine_id, protection.id, snapshot, path, stamp), {
      topic: "actions",
      onDone: (run) => {
        setStarted((before) => [{ id: run.id, destination }, ...before]);
        // Une nouvelle restauration du même chemin prend un nouvel horodatage.
        setStamp(restoreStamp(new Date()));
      },
    });
  };
  return (
    <div className="backup-restore">
      <span className="secondary">{t("backup.restore_beside", source.machine_name)}</span>
      <div>
        <ConfirmInline question={t("backup.restore_confirm", destination)} onConfirm={restore} icon="archive" disabled={action.busy}>
          {t("backup.restore")}
        </ConfirmInline>
      </div>
      <Failure error={action.error} gesture={{ machineID: source.machine_id }} />
      <RestoreResults machineID={source.machine_id} started={started} />
    </div>
  );
}

// Où en est chaque restauration lancée : l'état de l'ordre que la source
// rapporte, et la raison d'un refus ou d'un échec.
function RestoreResults({ machineID, started }: { machineID: string; started: { id: string; destination: string }[] }) {
  const t = useT();
  const actions = useMachineActions(machineID);
  if (started.length === 0) {
    return null;
  }
  const runs = actions.data?.runs ?? [];
  return (
    <ul className="backup-restore-results">
      {started.map((item) => {
        const run: ActionRun | undefined = runs.find((candidate) => candidate.id === item.id);
        const state = run?.state ?? "sent";
        return (
          <li key={item.id}>
            <Pill small tone={stateTones[state] ?? "neutral"}>
              {t(`action.state_${state}`)}
            </Pill>
            <span className="mono">{item.destination}</span>
            {run !== undefined && run.reason !== "" && <span className="secondary">{reasonText(t, run.reason)}</span>}
          </li>
        );
      })}
    </ul>
  );
}
