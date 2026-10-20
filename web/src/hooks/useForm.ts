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
  // Plusieurs champs d'un coup, par-dessus la saisie : voir useForm.
  update: (patch: Partial<Values>) => void;
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
//
// update écrit les champs qu'on lui passe et garde les autres. Deux usages :
// une suggestion venue d'ailleurs (la machine locale, le conteneur d'où part
// une sonde), qui complète la saisie sans l'effacer ; et, pour un formulaire
// qui reste affiché après l'enregistrement, ce que le serveur a gardé
// (passer toutes les valeurs est un cas particulier). Il ne touche pas au
// remplissage depuis la source : celui-ci reste unique, et une suggestion
// écrite avant l'arrivée de la source est remplacée par elle.
//
// Pourquoi pas un set(values) entier : la page ne voit que les valeurs de
// son dernier rendu, pas une écriture encore en attente (la machine locale
// posée dans le même passage) ; update ne réécrit que ses champs, sur les
// valeurs les plus récentes. Pourquoi pas un second Filling : une
// suggestion ne bloque pas l'enregistrement (isFilled), et une réponse du
// serveur n'arrive pas par la source.
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

  const update = (patch: Partial<Values>) => {
    setValues((current) => ({ ...current, ...patch }));
  };

  return { values, change, update, isFilled: filling === null || filled };
}

// La source remplit le formulaire à sa première arrivée, et jamais après.
export function shouldFill<Source>(filled: boolean, source: Source | null): source is Source {
  return !filled && source !== null;
}
