import type { FormEvent, ReactNode } from "react";

import type { ApiError } from "../api/client";
import { Button } from "./Button";
import { Card } from "./Card";
import { Failure } from "./Failure";
import type { IconName } from "./Icon";

// Le squelette d'un formulaire : les champs, l'erreur de l'écriture sous
// eux, puis les boutons. La page n'écrit que ce qui lui est propre.
export function FormCard({
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
    <Card className="form-card">
      <form className="card-b" onSubmit={submit}>
        {children}
        <Failure error={error} />
        {actions}
      </form>
    </Card>
  );
}

// Valider, annuler, et supprimer quand la page le propose. Valider attend
// que le formulaire soit rempli ; valider et supprimer attendent la fin de
// l'écriture en cours.
export function FormActions({
  submitLabel,
  submitIcon,
  busy,
  isFilled,
  cancelLabel,
  cancelTo,
  deleteLabel,
  onDelete,
}: {
  submitLabel: string;
  submitIcon: IconName;
  busy: boolean;
  isFilled: boolean;
  cancelLabel: string;
  cancelTo: string;
  deleteLabel?: string;
  onDelete?: (() => void) | undefined;
}) {
  return (
    <div className="cluster">
      <Button type="submit" variant="primary" icon={submitIcon} disabled={busy || !isFilled}>
        {submitLabel}
      </Button>
      <Button variant="ghost" to={cancelTo}>
        {cancelLabel}
      </Button>
      {onDelete !== undefined && (
        <Button variant="danger" icon="x" onClick={onDelete} disabled={busy}>
          {deleteLabel}
        </Button>
      )}
    </div>
  );
}
