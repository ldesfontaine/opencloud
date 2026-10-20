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
  // Après l'écriture, qu'elle ait réussi ou non, et après onDone : relire ce
  // qu'un refus a pu laisser changé (une alerte déjà résolue, un acquittement
  // à moitié passé), ou ce que le serveur a gardé malgré l'erreur.
  onSettled?: () => void;
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
    let outcome: Outcome<Result> = { done: false };
    try {
      outcome = { done: true, result: await perform() };
    } catch (failure) {
      setError(failureOf(failure));
    } finally {
      running.current = false;
      setBusy(false);
    }
    // Hors du try : une relecture ou une navigation qui échoue n'est pas
    // une écriture refusée.
    finishAction(refresh, options, outcome);
  };

  return {
    run: (perform, options) => void run(perform, options),
    busy,
    error,
  };
}

// Ce que l'écriture a rendu : sa réponse, ou rien si elle a été refusée.
export type Outcome<Result> = { done: true; result: Result } | { done: false };

// Ce qui suit l'écriture, dans l'ordre : la relecture du sujet et onDone
// seulement si elle a réussi, onSettled dans tous les cas.
export function finishAction<Result>(refresh: (topic?: Topic) => void, options: RunOptions<Result>, outcome: Outcome<Result>): void {
  if (outcome.done) {
    refreshAfter(refresh, options.topic);
    options.onDone?.(outcome.result);
  }
  options.onSettled?.();
}

export function refreshAfter(refresh: (topic?: Topic) => void, topic: Topic | "all" | undefined): void {
  if (topic === "all") {
    refresh();
  } else if (topic !== undefined) {
    refresh(topic);
  }
}
