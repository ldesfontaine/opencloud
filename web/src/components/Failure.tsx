import { errorKey, type ApiError } from "../api/client";
import { useT } from "../i18n/context";
import { rightOfRefusal } from "../lib/rights";
import { Note } from "./Card";
import { MissingRightButton } from "./MissingRight";

// Où un refus de droit mène : la machine dont l'agent manque du droit, le
// droit s'il ne se lit pas dans le refus, et le chemin qu'il devra couvrir.
export interface RightGesture {
  machineID: string;
  right?: string;
  path?: string;
}

// Une erreur se dit en note, jamais en pastille : la pastille dit l'état
// d'un objet. L'erreur arrive telle que l'API l'a rendue, ou déjà en clé du
// catalogue (les formulaires). Sans erreur, rien. Un refus de droit porte son
// geste (« Ré-enrôler avec ce droit ») dès que l'écran dit de quelle machine il
// s'agit : la cause est corrigée ici, pas dans chaque écran.
export function Failure({ error, gesture }: { error: ApiError | string | null | undefined; gesture?: RightGesture | undefined }) {
  const t = useT();
  if (error === null || error === undefined) {
    return null;
  }
  const key = typeof error === "string" ? error : errorKey(error);
  const right = gesture === undefined ? null : (gesture.right ?? rightOfRefusal(key));
  if (gesture === undefined || right === null) {
    return <Note tone="danger">{t(key)}</Note>;
  }
  return (
    <Note tone="danger">
      <p>{t(key)}</p>
      <MissingRightButton machineID={gesture.machineID} right={right} path={gesture.path} />
    </Note>
  );
}
