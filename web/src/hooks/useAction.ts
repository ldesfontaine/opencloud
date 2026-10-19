import { useRef, useState } from "react";

import { ApiError, failureOf } from "../api/client";
import { useRefresh, type Topic } from "../api/refresh";

export interface RunOptions<Result> {
  // Le texte à confirmer avant d'écrire ; sans lui, on écrit sans demander.
  confirm?: string;
  // Ce que l'écriture change, à relire après elle : un sujet du direct, ou tout.
  topic?: Topic | "all";
  // Après la relecture : naviguer, montrer « Enregistré », garder la réponse.
  onDone?: (result: Result) => void;
}

export interface Action {
  run: <Result>(perform: () => Promise<Result>, options?: RunOptions<Result>) => void;
  // Pour l'affichage seulement : la garde contre le double clic est dans run.
  busy: boolean;
  error: ApiError | null;
}

// Les écritures d'un bloc de boutons : une à la fois, une erreur pour le
// bloc. perform construit son chemin d'API lui-même, dans la promesse : un
// segment() qui lève devient une erreur affichée, jamais un bouton bloqué.
export function useAction(): Action {
  const { refresh } = useRefresh();
  const running = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const run = async <Result>(perform: () => Promise<Result>, options: RunOptions<Result> = {}) => {
    if (running.current) {
      return;
    }
    if (options.confirm !== undefined && !window.confirm(options.confirm)) {
      return;
    }
    running.current = true;
    setError(null);
    setBusy(true);
    let result: Result;
    try {
      result = await perform();
    } catch (failure) {
      setError(failureOf(failure));
      return;
    } finally {
      running.current = false;
      setBusy(false);
    }
    // Hors du try : une relecture ou une navigation qui échoue n'est pas
    // une écriture refusée.
    refreshAfter(refresh, options.topic);
    options.onDone?.(result);
  };

  return {
    run: (perform, options) => void run(perform, options),
    busy,
    error,
  };
}

export function refreshAfter(refresh: (topic?: Topic) => void, topic: Topic | "all" | undefined): void {
  if (topic === "all") {
    refresh();
  } else if (topic !== undefined) {
    refresh(topic);
  }
}
