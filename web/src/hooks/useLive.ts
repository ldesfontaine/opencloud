import { useEffect } from "react";

import { useRefresh } from "../lib/refresh";

// Le direct : une seule connexion par onglet, ouverte par la coquille. Le
// serveur n'envoie que le nom de ce qui a changé ; on relit. L'EventSource
// natif revient seul après une coupure, avec Last-Event-ID : le serveur
// répond « reconnected » et on relit tout, car rien n'est rejoué.
export function useLive(): void {
  const { refresh } = useRefresh();
  useEffect(() => {
    const source = new EventSource("/api/events");
    const onMachines = () => refresh("machines");
    const onJobs = () => refresh("jobs");
    const onResources = () => refresh("resources");
    const onServices = () => refresh("services");
    const onReconnected = () => refresh();
    source.addEventListener("machines", onMachines);
    source.addEventListener("jobs", onJobs);
    source.addEventListener("resources", onResources);
    source.addEventListener("services", onServices);
    source.addEventListener("reconnected", onReconnected);
    return () => source.close();
  }, [refresh]);
}
