import { useState, type ReactNode } from "react";

import { deleteBackupPlan, deleteResticBackup, runBackupNow, runResticBackup, verifyResticBackup, type BackupProtection, type BackupSource } from "../../api/backups";
import { errorKey } from "../../api/client";
import { useOfflineMachines } from "../../components/AgentGate";
import { ConfirmDialog } from "../../components/ConfirmModal";
import { type MenuAction } from "../../components/MoreMenu";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { browsePath, editPlanPath, editResticPath, keyringPath, newDestinationPath } from "../../lib/backups";
import { holderOfRefusal } from "../../lib/rights";

// Les gestes d'une protection, écrits une fois : la carte les met dans son
// menu « … », l'inspecteur les met en boutons. Une écriture à la fois ; le
// refus d'un droit se corrige sur la machine qui le manque (la source, ou la
// cible) ; les confirmations sont des fenêtres que l'appelant place où il veut.
export function useProtectionGestures(source: BackupSource, protection: BackupProtection): { menu: MenuAction[]; error: ReturnType<typeof useAction>["error"]; refusedHolder: string; dialogs: ReactNode } {
  const t = useT();
  const action = useAction();
  const [deleting, setDeleting] = useState(false);
  const refusedHolder = action.error === null ? source.machine_id : holderOfRefusal(errorKey(action.error), { source: source.machine_id, target: protection.target_machine_id });
  const planTitle = protection.name === "" ? source.name : protection.name;
  // Un plan d'archives a besoin de sa destination, et de la machine qui produit : hors ligne,
  // le geste reste au menu, grisé, avec sa raison (jamais caché).
  const needed = [protection.target_machine_id, ...(source.kind === "database" || source.kind === "firewall" ? [] : [source.machine_id])];
  const offline = (useOfflineMachines(needed) ?? []).length > 0;

  let menu: MenuAction[] = [];
  if (protection.origin === "archive") {
    menu = [
      { label: t("backup.run_now"), disabled: offline, ...(offline ? { hint: t("agent.offline_reason") } : {}), onSelect: () => action.run(() => runBackupNow(protection.id), { topic: "backups" }) },
      { label: t("backup.plan_edit"), to: editPlanPath(protection.id) },
      { label: t("backup.plan_delete"), danger: true, onSelect: () => setDeleting(true) },
    ];
  } else if (protection.origin === "restic") {
    // « Sauvegarder maintenant » reste offert pendant un passage : le serveur
    // refuse alors en le disant (restic.backup_running).
    menu = [
      { label: t("backup.browse"), to: browsePath(source.machine_id, protection.id) },
      { label: t("backup.run_now"), onSelect: () => action.run(() => runResticBackup(source.machine_id, protection.id), { topic: "backups" }) },
      { label: t("backup.verify_now"), onSelect: () => action.run(() => verifyResticBackup(source.machine_id, protection.id), { topic: "backups" }) },
      { label: t("backup.restic_edit"), to: editResticPath(source.machine_id, protection.id) },
      { label: t("backup.restic_delete"), danger: true, onSelect: () => setDeleting(true) },
    ];
  }
  if (source.kind === "database") {
    menu = [...menu, { label: t("backup.keyring_title"), to: keyringPath }, { label: t("backup.destination_add"), to: newDestinationPath }];
  }

  const dialogs = deleting ? (
    protection.origin === "archive" ? (
      <ConfirmDialog
        title={t("backup.plan_delete")}
        question={t("backup.plan_delete_confirm", planTitle)}
        confirmLabel={t("backup.plan_delete")}
        onConfirm={() => action.run(() => deleteBackupPlan(protection.id), { topic: "backups" })}
        onClose={() => setDeleting(false)}
      />
    ) : (
      <ConfirmDialog
        title={t("backup.restic_delete")}
        question={t("backup.restic_delete_confirm", source.name, protection.target_machine_name)}
        confirmLabel={t("backup.restic_delete")}
        onConfirm={() => action.run(() => deleteResticBackup(source.machine_id, protection.id), { topic: "backups" })}
        onClose={() => setDeleting(false)}
      />
    )
  ) : null;
  return { menu, error: action.error, refusedHolder, dialogs };
}
