import { Fragment } from "react";
import { Link } from "react-router";

import { splitLinks } from "../lib/richText";

// Un texte du catalogue dont les renvois de menu sont de vrais liens. Les
// composants qui montrent une phrase (note, aide d'un champ, carte vide)
// passent par lui : un écran n'a pas à y penser.
export function Rich({ text }: { text: string }) {
  return (
    <>
      {splitLinks(text).map((part, index) =>
        part.kind === "link" ? (
          <Link key={index} to={part.to}>
            {part.label}
          </Link>
        ) : (
          <Fragment key={index}>{part.text}</Fragment>
        ),
      )}
    </>
  );
}
