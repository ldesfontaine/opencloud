import { fetchProposal, ignoreObservation, saveDeclaredService, unignoreObservation, type IgnoreKey } from "../api/directory";
import { useRefresh } from "../api/refresh";
import { acceptanceOf } from "../lib/directory";
import { useAction } from "./useAction";

// Les trois gestes d'une proposition, pour la liste de Services comme pour la
// fiche d'un conteneur : accepter, ignorer, ne plus ignorer. Accepter relit la
// proposition à l'instant d'écrire puis l'envoie par le chemin unique de la
// déclaration ; si elle a changé entre-temps (ignorée ou déclarée ailleurs),
// le serveur refuse, l'erreur s'affiche et la liste se relit.
export function useProposalActions() {
  const { refresh } = useRefresh();
  const action = useAction();
  // Relire après un refus aussi : c'est le cas où la liste était périmée.
  const options = { onSettled: () => refresh("directory") };
  return {
    accept: (containerID: string) =>
      action.run(async () => {
        return saveDeclaredService(undefined, acceptanceOf(await fetchProposal(containerID)));
      }, options),
    ignore: (key: IgnoreKey) => action.run(() => ignoreObservation(key), options),
    stopIgnoring: (key: IgnoreKey) => action.run(() => unignoreObservation(key), options),
    busy: action.busy,
    error: action.error,
  };
}
