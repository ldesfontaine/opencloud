import { useEffect, useRef, useState } from "react";

import { failureOf, type ApiError } from "../api/client";
import { planOfRefusal, type DeletionPlan } from "../api/deletion";
import type { Topic } from "../api/refresh";
import { useAction } from "../hooks/useAction";
import { useI18n, type Translate } from "../i18n/context";
import { blockerText, countsText, isBarePlan } from "../lib/deletion";
import { Button } from "./Button";
import { Note } from "./Card";
import { Failure } from "./Failure";
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
  const dialog = useRef<HTMLDialogElement>(null);
  const action = useAction();
  const [loaded, setLoaded] = useState<DeletionPlan | null>(null);
  const [loadError, setLoadError] = useState<ApiError | null>(null);

  // Le plan se lit une fois, à l'ouverture : la boîte est montée pour lui,
  // et la page la démonte en la fermant. loadPlan est une flèche refaite à
  // chaque rendu de la page : la suivre relirait le plan sans fin.
  useEffect(() => {
    if (dialog.current !== null && !dialog.current.open) {
      dialog.current.showModal();
    }
    loadPlan().then(setLoaded, (failure: unknown) => setLoadError(failureOf(failure)));
  }, []);

  const plan = planOfRefusal(action.error) ?? loaded;
  const confirm = () => {
    action.run(perform, { topic, onDone });
  };

  return (
    <dialog ref={dialog} className="delete-plan" onClose={onClose}>
      <p className="delete-plan-question">{question}</p>
      <Failure error={loadError ?? action.error} />
      {plan === null && loadError === null && <p className="muted">{t("deletion.loading")}</p>}
      {plan !== null && <PlanBody plan={plan} t={t} language={language} />}
      <div className="delete-plan-actions">
        <Button onClick={() => dialog.current?.close()}>{t("deletion.cancel")}</Button>
        <Button variant="danger" icon="x" onClick={confirm} disabled={plan === null || plan.blocked || action.busy}>
          {confirmLabel}
        </Button>
      </div>
    </dialog>
  );
}

function PlanBody({ plan, t, language }: { plan: DeletionPlan; t: Translate; language: string }) {
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
