import type { FormEvent, ReactNode } from "react";

import type { ApiError } from "../api/client";
import { Button } from "./Button";
import { Failure } from "./Failure";
import type { IconName } from "./Icon";

// Le corps d'un formulaire qui reste dans une carte de la page : les champs,
// l'erreur de l'écriture sous eux, puis les boutons. Il sert les formulaires
// qui partagent leur carte avec d'autres (l'accès à l'interface, le fil d'un
// incident). Un formulaire qui s'ajoute ou se corrige s'ouvre en fenêtre :
// ModalForm ou Wizard.
export function FormBody({
  onSubmit,
  error,
  actions,
  children,
}: {
  onSubmit: () => void;
  error: ApiError | string | null;
  actions: ReactNode;
  children: ReactNode;
}) {
  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSubmit();
  };
  return (
    <form className="card-b" onSubmit={submit}>
      {children}
      <Failure error={error} />
      {actions}
    </form>
  );
}

// Valider. canSubmit attend que la saisie le permette ; busy, la fin de
// l'écriture en cours.
export function FormActions({ submitLabel, submitIcon, busy, canSubmit = true }: { submitLabel: string; submitIcon: IconName; busy: boolean; canSubmit?: boolean }) {
  return (
    <div className="cluster">
      <Button type="submit" variant="primary" icon={submitIcon} disabled={busy || !canSubmit}>
        {submitLabel}
      </Button>
    </div>
  );
}
