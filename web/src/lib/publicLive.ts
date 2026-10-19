// Le direct de la page de statut publique et son repli. Le serveur borne
// les flux publics (quelques-uns par source, 256 en tout, dix minutes
// chacun) : un visiteur refusé ou coupé ne doit pas rester figé sur un état
// périmé. Sans le direct, la page se relit toutes les minutes et retente le
// flux toutes les cinq.
export const pollEvery = 60_000;
export const reopenEvery = 5 * 60_000;

// EventSource.CLOSED : le navigateur ne reviendra pas de lui-même.
const streamClosed = 2;

// Ce qu'on utilise d'un EventSource ; les tests en passent un faux.
export type LiveStream = {
  readonly readyState: number;
  addEventListener(type: string, listener: () => void): void;
  close(): void;
};

// followPublicLive ouvre le direct, se replie sur des relectures quand il
// tombe, et rend la fonction qui arrête tout (flux et minuteries).
export function followPublicLive(openStream: () => LiveStream, reload: () => void): () => void {
  let stream: LiveStream | null = null;
  let reopenTimer: ReturnType<typeof setTimeout> | undefined;
  let pollTimer: ReturnType<typeof setInterval> | undefined;

  const stopPolling = () => {
    clearInterval(pollTimer);
    pollTimer = undefined;
  };
  const startPolling = () => {
    pollTimer ??= setInterval(reload, pollEvery);
  };
  const open = () => {
    stream?.close();
    const current = openStream();
    stream = current;
    // Le direct revient après un temps de relecture : une relecture de plus
    // couvre ce qui a changé entre la dernière et lui.
    current.addEventListener("connected", () => {
      const wasPolling = pollTimer !== undefined;
      stopPolling();
      if (wasPolling) {
        reload();
      }
    });
    current.addEventListener("status", reload);
    current.addEventListener("reconnected", () => {
      stopPolling();
      reload();
    });
    current.addEventListener("error", () => {
      startPolling();
      // Refusé (limite de la source, pool plein) : le navigateur ne reviendra
      // pas, on retente plus tard. Sinon il se reconnecte seul, la fin d'un
      // flux public à son échéance y compris.
      if (current.readyState === streamClosed) {
        clearTimeout(reopenTimer);
        reopenTimer = setTimeout(open, reopenEvery);
      }
    });
  };

  open();
  return () => {
    clearTimeout(reopenTimer);
    stopPolling();
    stream?.close();
  };
}
