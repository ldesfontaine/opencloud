import { errorKey } from "../api/client";
import { useT } from "../i18n/context";
import { Note } from "./Card";

// Une erreur dite à l'opérateur dans sa langue : la clé du serveur si c'est
// un refus, sinon le message générique.
export function Failure({ error }: { error: unknown }) {
  const t = useT();
  return <Note tone="danger">{t(errorKey(error))}</Note>;
}
