import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

interface Refresh {
  // Change à chaque demande de rafraîchissement ; les ressources l'écoutent.
  version: number;
  refresh: () => void;
}

const RefreshContext = createContext<Refresh>({ version: 0, refresh: () => {} });

// Un seul signal pour tout recharger : après une action de l'opérateur
// aujourd'hui, à l'arrivée d'un événement du direct demain.
export function RefreshProvider({ children }: { children: ReactNode }) {
  const [version, setVersion] = useState(0);
  const refresh = useCallback(() => setVersion((current) => current + 1), []);
  const value = useMemo(() => ({ version, refresh }), [version, refresh]);
  return <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>;
}

export function useRefresh(): Refresh {
  return useContext(RefreshContext);
}
