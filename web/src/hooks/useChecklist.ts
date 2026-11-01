import { useState } from "react";

// Ce qui est coché dans une `Checklist`, tenu par la page ou l'assistant qui
// la montre : une étape quittée n'oublie rien.
export function useChecklist() {
  const [done, setDone] = useState<string[]>([]);
  const toggle = (id: string, checked: boolean) => setDone((current) => (checked ? [...current.filter((item) => item !== id), id] : current.filter((item) => item !== id)));
  return { done, toggle };
}
