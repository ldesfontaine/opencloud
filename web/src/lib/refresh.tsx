import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

// Les sujets du direct, tels que le serveur les nomme.
export type Topic = "machines" | "jobs";

type Versions = Record<Topic, number>;

interface Refresh {
  // Relit un sujet, ou tout sans argument.
  refresh: (topic?: Topic) => void;
  // Change quand une ressource de ce chemin doit être relue.
  versionFor: (path: string) => number;
}

const RefreshContext = createContext<Refresh>({ refresh: () => {}, versionFor: () => 0 });

// Le chemin d'une ressource dit son sujet ; les compteurs relèvent des deux.
function topicsOf(path: string): Topic[] {
  if (path.startsWith("/api/machines")) {
    return ["machines"];
  }
  if (path.startsWith("/api/jobs")) {
    return ["jobs"];
  }
  return ["machines", "jobs"];
}

// Un seul signal pour relire : après une action de l'opérateur, ou à
// l'arrivée d'un sujet du direct. Chaque sujet a son compteur, pour que la
// page des tâches ne relise pas quand une machine signale.
export function RefreshProvider({ children }: { children: ReactNode }) {
  const [versions, setVersions] = useState<Versions>({ machines: 0, jobs: 0 });
  const refresh = useCallback((topic?: Topic) => {
    setVersions((current) => ({
      machines: topic === undefined || topic === "machines" ? current.machines + 1 : current.machines,
      jobs: topic === undefined || topic === "jobs" ? current.jobs + 1 : current.jobs,
    }));
  }, []);
  const value = useMemo<Refresh>(
    () => ({
      refresh,
      versionFor: (path) => topicsOf(path).reduce((sum, topic) => sum + versions[topic], 0),
    }),
    [versions, refresh],
  );
  return <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>;
}

export function useRefresh(): Refresh {
  return useContext(RefreshContext);
}
