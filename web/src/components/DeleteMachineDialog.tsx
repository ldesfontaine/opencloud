import { useState } from "react";

import { fetchMachineDeletePlan } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useI18n } from "../i18n/context";
import { canConfirmDeletion } from "../lib/machines";
import { Button } from "./Button";
import { Failure } from "./Failure";
import { Checkbox, Field, Input } from "./Field";
import { PlanBody, useDeletionPlan } from "./DeletePlanDialog";
import { Modal, ModalCancel } from "./Modal";
import "./DeletePlanDialog.scss";

interface Props {
  machine: { id: string; name: string };
  // Le choix de l'historique quand il est déjà fait : une demande de jeton le
  // porte, l'opérateur le voit sans pouvoir le changer. Absent, il le choisit.
  fixedKeepHistory?: boolean;
  // Déjà archivée : garder l'historique n'a plus de sens, seule l'issue
  // définitive reste.
  archived?: boolean;
  // Garder l'historique archive la machine ; sinon elle est effacée.
  perform: (keepHistory: boolean) => Promise<void>;
  // Rend l'issue choisie : archivée (true) ou effacée (false), pour que la page
  // sache si la machine est encore là.
  onDone: (keptHistory: boolean) => void;
  onClose: () => void;
}

// « Supprimer la machine » : un geste, deux issues. Garder l'historique (par
// défaut) archive la machine, son agent est coupé et rien n'est effacé ;
// ré-enrôler la remet en service. Sans historique, le plan dit ce qui retient
// la machine et ce qui part avec elle, et le nom de la machine se tape, comme
// pour tout ce qui ne se défait pas. Le plan ne concerne que cette issue ;
// une machine déjà archivée n'a plus qu'elle.
export function DeleteMachineDialog({ machine, fixedKeepHistory, archived = false, perform, onDone, onClose }: Props) {
  const { t, language } = useI18n();
  const action = useAction();
  const [chosenKeepHistory, setKeepHistory] = useState(true);
  const [typedName, setTypedName] = useState("");
  const { plan, loadError } = useDeletionPlan(() => fetchMachineDeletePlan(machine.id), action.error);
  const keepHistory = fixedKeepHistory ?? (!archived && chosenKeepHistory);
  const blocked = !keepHistory && (plan === null || plan.blocked);
  const confirm = () => {
    action.run(() => perform(keepHistory), { topic: "all", onDone: () => onDone(keepHistory) });
  };

  return (
    <Modal
      title={t("machine.delete")}
      onClose={onClose}
      dirty={typedName !== ""}
      actions={
        <>
          <ModalCancel />
          <Button variant="danger" icon="x" onClick={confirm} disabled={blocked || !canConfirmDeletion(machine.name, typedName, keepHistory) || action.busy}>
            {t(keepHistory ? "machine.delete_archive" : "machine.delete_erase")}
          </Button>
        </>
      }
    >
      <p className="delete-plan-question">{t("machine.delete_question", machine.name)}</p>
      {archived ? null : fixedKeepHistory === undefined ? (
        <Checkbox label={t("machine.delete_keep")} checked={keepHistory} onChange={setKeepHistory} />
      ) : (
        <p className="delete-plan-line">
          <b>{t(keepHistory ? "machine.delete_requested_keep" : "machine.delete_requested_erase")}</b>
        </p>
      )}
      <p className="delete-plan-line">{t(keepHistory ? "machine.delete_keep_text" : "machine.delete_erase_text")}</p>
      <Failure error={loadError ?? action.error} />
      {!keepHistory && plan === null && loadError === null && <p className="muted">{t("deletion.loading")}</p>}
      {!keepHistory && plan !== null && <PlanBody plan={plan} t={t} language={language} />}
      {!keepHistory && (
        <Field label={t("machine.delete_name_label", machine.name)} htmlFor="delete-machine-name">
          <Input id="delete-machine-name" type="text" value={typedName} onChange={(event) => setTypedName(event.target.value)} autoComplete="off" />
        </Field>
      )}
    </Modal>
  );
}
