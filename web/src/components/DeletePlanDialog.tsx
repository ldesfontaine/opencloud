import { useEffect, useState } from "react";

import { failureOf, type ApiError } from "../api/client";
import { planOfRefusal, type DeletionPlan } from "../api/deletion";
import type { Topic } from "../api/refresh";
import { useAction } from "../hooks/useAction";
import { useI18n, type Translate } from "../i18n/context";
import { blockerText, countsText, isBarePlan } from "../lib/deletion";
import { Button } from "./Button";
import { Note } from "./Card";
import { Failure } from "./Failure";
import { Modal, ModalCancel } from "./Modal";
import "./DeletePlanDialog.scss";

interface Props {
  // La question, déjà traduite : « Retirer vps-paris-1 ? ».
  question: string;
  // Le bouton qui supprime : « Supprimer », « Retirer ».
  confirmLabel: string;
  loadPlan: () => Promise<DeletionPlan>;
  perform: () => Promise<void>;
  topic: Topic | "all";
  onDone: () => void;
  onClose: () => void;
}

// La confirmation d'une suppression montre son plan avant d'écrire : ce qui
// la retient (le bouton reste fermé), ce qui part avec l'objet, ce qui reste
// détaché. Rien ne part en silence. Un refus reçu quand même (un lien posé
// entre-temps) remplace le plan par celui que le serveur rend.
export function DeletePlanDialog({ question, confirmLabel, loadPlan, perform, topic, onDone, onClose }: Props) {
  const { t, language } = useI18n();
  const action = useAction();
  const { plan, loadError } = useDeletionPlan(loadPlan, action.error);
  const confirm = () => {
    action.run(perform, { topic, onDone });
  };

  return (
    <Modal
      title={confirmLabel}
      onClose={onClose}
      actions={
        <>
          <ModalCancel />
          <Button variant="danger" icon="x" onClick={confirm} disabled={plan === null || plan.blocked || action.busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <p className="delete-plan-question">{question}</p>
      <Failure error={loadError ?? action.error} />
      {plan === null && loadError === null && <p className="muted">{t("deletion.loading")}</p>}
      {plan !== null && <PlanBody plan={plan} t={t} language={language} />}
    </Modal>
  );
}

// Le plan d'une suppression, lu une fois à l'ouverture de la fenêtre : elle
// est montée pour lui, et la page la démonte en la fermant. loadPlan est une
// flèche refaite à chaque rendu de la page : la suivre relirait le plan sans
// fin. Un refus de la suppression (error) rend le plan d'aujourd'hui, qui
// remplace celui qu'on avait lu.
export function useDeletionPlan(loadPlan: () => Promise<DeletionPlan>, error: ApiError | null): { plan: DeletionPlan | null; loadError: ApiError | null } {
  const [loaded, setLoaded] = useState<DeletionPlan | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);
  useEffect(() => {
    loadPlan().then(setLoaded, (failure: unknown) => setLoadError(failureOf(failure)));
  }, []);
  return { plan: planOfRefusal(error) ?? loaded, loadError };
}

export function PlanBody({ plan, t, language }: { plan: DeletionPlan; t: Translate; language: string }) {
  if (isBarePlan(plan)) {
    return <p className="muted">{t("deletion.nothing")}</p>;
  }
  return (
    <>
      {plan.blocked && (
        <Note tone="danger">
          <b>{t("deletion.blocked")}</b>
          <ul className="delete-plan-list">
            {plan.blockers.map((blocker) => (
              <li key={blocker.kind}>{blockerText(t, language, blocker)}</li>
            ))}
          </ul>
        </Note>
      )}
      {plan.removes.length > 0 && (
        <p className="delete-plan-line">
          <b>{t("deletion.removes")}</b> {countsText(t, language, plan.removes)}
        </p>
      )}
      {plan.detaches.length > 0 && (
        <p className="delete-plan-line">
          <b>{t("deletion.detaches")}</b> {countsText(t, language, plan.detaches)}
        </p>
      )}
    </>
  );
}
