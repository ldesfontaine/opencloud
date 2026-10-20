import { useEffect, useState } from "react";

// D'où un formulaire d'édition tire ses valeurs : la ressource lue, null
// tant qu'elle n'est pas arrivée, et sa traduction en valeurs (jobFormOf,
// probeFormOf…).
export interface Filling<Source, Values> {
  source: Source | null;
  fill: (source: Source) => Values;
}

export interface Form<Values> {
  values: Values;
  change: <Key extends keyof Values>(key: Key, value: Values[Key]) => void;
  // Faux tant qu'un formulaire d'édition attend sa source : l'enregistrer
  // écraserait l'objet avec les valeurs vides.
  isFilled: boolean;
}

// Les valeurs d'un formulaire. Sans filling (création), il part de empty et
// il est prêt tout de suite. Avec un filling (édition), il attend la source
// et s'en remplit une seule fois : une relecture du direct rend un objet
// neuf, elle ne doit pas écraser ce que l'opérateur est en train de taper.
//
// Le filling est un objet à part plutôt qu'une source « null ou undefined » :
// « rien à attendre » et « pas encore arrivé » se lisent sans connaître la
// différence entre les deux.
export function useForm<Values, Source = never>(empty: Values, filling: Filling<Source, Values> | null = null): Form<Values> {
  const [values, setValues] = useState<Values>(empty);
  const [filled, setFilled] = useState(false);
  const source = filling === null ? null : filling.source;

  // fill n'est pas une dépendance : la page en rend une nouvelle à chaque
  // rendu, et seule l'arrivée de la source doit remplir.
  useEffect(() => {
    if (filling === null || !shouldFill(filled, source)) {
      return;
    }
    setFilled(true);
    setValues(filling.fill(source));
  }, [source, filled]);

  const change = <Key extends keyof Values>(key: Key, value: Values[Key]) => {
    setValues((current) => ({ ...current, [key]: value }));
  };

  return { values, change, isFilled: filling === null || filled };
}

// La source remplit le formulaire à sa première arrivée, et jamais après.
export function shouldFill<Source>(filled: boolean, source: Source | null): source is Source {
  return !filled && source !== null;
}
