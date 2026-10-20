import { useEffect } from "react";

import { topics, useRefresh } from "../api/refresh";

// Le direct : une seule connexion par onglet, ouverte par la coquille. Le
// serveur n'envoie que le nom de ce qui a changé ; on relit. L'EventSource
// natif revient seul après une coupure, avec Last-Event-ID : le serveur
// répond « reconnected » et on relit tout, car rien n'est rejoué.
export function useLive(): void {
  const { refresh } = useRefresh();
  useEffect(() => {
    const source = new EventSource("/api/events");
    for (const topic of topics) {
      source.addEventListener(topic, () => refresh(topic));
    }
    source.addEventListener("reconnected", () => refresh());
    return () => source.close();
  }, [refresh]);
}
