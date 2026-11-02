import { useMemo } from "react";
import { useNavigate, useParams } from "react-router";

import { browseResticBackup, useBackupOverview, type BackupProtection, type BackupSource } from "../../api/backups";
import { Button } from "../../components/Button";
import { TokenBox } from "../../components/Copy";
import { FileExplorer, type FileOutput, type FileSource } from "../../components/FileExplorer";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { useI18n, type Translate } from "../../i18n/context";
import { backupsPath } from "../../lib/backups";
import { repositoryUser, resticAccesses, restoreCommand } from "../../lib/backupOverview";
import { childPath } from "../../lib/fileExplorer";
import { formatWhen } from "../../lib/time";

// « Parcourir ses sauvegardes » : la fenêtre de la page Sauvegardes, à
// /sauvegardes/parcourir/<machine>/<sauvegarde>. Elle monte l'explorateur
// générique sur une source « instantané restic » : la cible lit son dépôt (ordre
// restic.browse) et rend des noms, des tailles et des dates ; rien ne se
// restaure d'ici. La sortie est la commande qui sort la sélection à côté, à
// lancer sur la machine source, qui garde le mot de passe du dépôt.
export function ExplorerModal() {
  const { machineId = "", backupId = "" } = useParams();
  const navigate = useNavigate();
  const overview = useBackupOverview();
  const access = resticAccesses(overview.data?.sources ?? []).find((candidate) => candidate.source.machine_id === machineId && candidate.protection.id === backupId);
  if (overview.data === null) {
    return null;
  }
  return <ExplorerDialog source={access?.source} protection={access?.protection} machineId={machineId} backupId={backupId} onClose={() => void navigate(backupsPath, { replace: true })} />;
}

function ExplorerDialog({ source, protection, machineId, backupId, onClose }: { source: BackupSource | undefined; protection: BackupProtection | undefined; machineId: string; backupId: string; onClose: () => void }) {
  const { t } = useI18n();
  const snapshots = useMemo<FileSource>(() => resticSnapshotSource(machineId, backupId), [machineId, backupId]);
  const outputs = useMemo<FileOutput[]>(() => (source === undefined || protection === undefined ? [] : [restoreBesideOutput(t, source, protection)]), [t, source, protection]);
  if (source === undefined || protection === undefined) {
    return (
      <Modal title={t("backup.explorer_title", "")} onClose={onClose}>
        <p className="secondary">{t("backup.explorer_gone")}</p>
      </Modal>
    );
  }
  return (
    <Modal
      title={t("backup.explorer_title", source.kind === "database" ? t("backup.source_database") : source.name)}
      onClose={onClose}
      wide
      actions={<Button onClick={onClose}>{t("modal.close")}</Button>}
    >
      <div className="backup-explorer-head">
        <Pill small tone={protection.path?.path === "relay" ? "warn" : "ok"}>
          {protection.target_machine_name} · {protection.path === null ? t("backup.flow_restic") : t(`backup.path_${protection.path.path}`)}
        </Pill>
        <span className="secondary">{t("backup.explorer_readonly")}</span>
      </div>
      <FileExplorer source={snapshots} outputs={outputs} rootLabel={source.name} viewsLabel={t("backup.explorer_snapshots")} gesture={{ machineID: protection.target_machine_id }} />
    </Modal>
  );
}

// La source « instantané restic » : les vues sont les instantanés du dépôt,
// plus récent d'abord ; un dossier se lit chez la cible, à chaque ouverture.
function resticSnapshotSource(machineId: string, backupId: string): FileSource {
  return {
    loadViews: async () => {
      const report = await browseResticBackup(machineId, backupId, "", "");
      return report.snapshots.map((snapshot) => ({
        id: snapshot.id,
        label: formatWhen(snapshot.time, Date.now()),
        hint: `${snapshot.id.slice(0, 8)} · ${snapshot.paths.join(", ")}`,
      }));
    },
    list: async (viewID, folder) => {
      const report = await browseResticBackup(machineId, backupId, viewID, folder);
      return { entries: report.entries.map((entry) => ({ name: entry.name, kind: entry.kind, size: entry.size, modified: entry.mtime })), truncated: report.truncated };
    },
  };
}

// Sortir la sélection à côté, sans rien écraser : l'environnement du dépôt, puis
// la commande. Sans instantané choisi, la sortie attend.
function restoreBesideOutput(t: Translate, source: BackupSource, protection: BackupProtection): FileOutput {
  const link = repositoryUser(protection.target_machine_id);
  return {
    id: "restore-beside",
    label: t("backup.explorer_restore", source.machine_name),
    render: (selection) => {
      if (selection.view === null) {
        return <span className="secondary">{t("backup.explorer_pick_snapshot")}</span>;
      }
      const paths = selection.names.length === 0 ? [selection.folder] : selection.names.map((name) => childPath(selection.folder, name));
      return (
        <>
          <span className="secondary">{t("backup.explorer_restore_env", source.machine_name)}</span>
          <TokenBox value={t("backup.access_env", link)} />
          <TokenBox value={restoreCommand(selection.view.id, selection.folder === "/" && selection.names.length === 0 ? [] : paths)} />
        </>
      );
    },
  };
}
