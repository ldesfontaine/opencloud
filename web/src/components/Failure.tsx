import { errorKey, type ApiError } from "../api/client";
import { useT } from "../i18n/context";
import { Note } from "./Card";

// Une erreur se dit en note, jamais en pastille : la pastille dit l'état
// d'un objet. L'erreur arrive telle que l'API l'a rendue, ou déjà en clé du
// catalogue (les formulaires). Sans erreur, rien.
export function Failure({ error }: { error: ApiError | string | null | undefined }) {
  const t = useT();
  if (error === null || error === undefined) {
    return null;
  }
  return <Note tone="danger">{t(typeof error === "string" ? error : errorKey(error))}</Note>;
}
