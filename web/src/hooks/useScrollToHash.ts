import { useEffect } from "react";
import { useLocation } from "react-router";

// Un lien « #ancre » mène à son bloc sur la page même : react-router change
// l'adresse sans que le navigateur défile. `ready` dit que la page est
// affichée (l'ancre peut arriver après la première image).
export function useScrollToHash(ready = true): void {
  const { hash } = useLocation();
  useEffect(() => {
    if (ready && hash !== "") {
      document.getElementById(decodeURIComponent(hash.slice(1)))?.scrollIntoView({ block: "start" });
    }
  }, [hash, ready]);
}
