import type { FormEvent, ReactNode } from "react";

import type { ApiError } from "../api/client";
import { Button } from "./Button";
import { Card } from "./Card";
import { Failure } from "./Failure";
import type { IconName } from "./Icon";

// Le squelette d'un formulaire : les champs, l'erreur de l'écriture sous
// eux, puis les boutons. La page n'écrit que ce qui lui est propre.
// header : un titre de carte au-dessus du formulaire ; width : s'arrêter à
// 560 px ou remplir la cellule d'une grille.
export function FormCard({
  onSubmit,
  error,
  actions,
  header,
  width = "form",
  children,
}: {
  onSubmit: () => void;
  error: ApiError | string | null;
  actions: ReactNode;
  header?: ReactNode;
  width?: "form" | "fill";
  children: ReactNode;
}) {
  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSubmit();
  };
  return (
    <Card className={width === "form" ? "form-card" : ""}>
      {header}
      <form className="card-b" onSubmit={submit}>
        {children}
        <Failure error={error} />
        {actions}
      </form>
    </Card>
  );
}

// Valider, annuler quand la page le propose (un lien avec cancelTo, un
// rappel avec onCancel), et supprimer. Valider attend que la saisie le
// permette (canSubmit) et la fin de l'écriture en cours ; supprimer attend
// la fin de l'écriture en cours.
export function FormActions({
  submitLabel,
  submitIcon,
  busy,
  canSubmit,
  cancelLabel,
  cancelTo,
  onCancel,
  deleteLabel,
  onDelete,
}: {
  submitLabel: string;
  submitIcon: IconName;
  busy: boolean;
  canSubmit: boolean;
  cancelLabel?: string;
  cancelTo?: string;
  onCancel?: () => void;
  deleteLabel?: string;
  onDelete?: (() => void) | undefined;
}) {
  return (
    <div className="cluster">
      <Button type="submit" variant="primary" icon={submitIcon} disabled={busy || !canSubmit}>
        {submitLabel}
      </Button>
      {cancelTo !== undefined && (
        <Button variant="ghost" to={cancelTo}>
          {cancelLabel}
        </Button>
      )}
      {cancelTo === undefined && onCancel !== undefined && (
        <Button variant="ghost" onClick={onCancel}>
          {cancelLabel}
        </Button>
      )}
      {onDelete !== undefined && (
        <Button variant="danger" icon="x" onClick={onDelete} disabled={busy}>
          {deleteLabel}
        </Button>
      )}
    </div>
  );
}
