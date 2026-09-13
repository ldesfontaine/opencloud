import { useEffect, useState } from "react";

// L'instant courant, rafraîchi à intervalle : « vu il y a 12 s » avance
// tout seul, sans rien redemander au serveur.
export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return now;
}
