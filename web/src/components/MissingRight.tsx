import type { ReactNode } from "react";

import { useT } from "../i18n/context";
import { capabilityLabelKey } from "../lib/machines";
import { reenrollPath } from "../lib/rights";
import { Button } from "./Button";
import { Note } from "./Card";

// Le geste d'un droit qui manque : le bouton qui ouvre « Ré-enrôler » avec ce
// droit déjà coché. Un refus de droit le porte partout où il se dit, pour que
// l'opérateur ne cherche jamais où changer les droits d'un agent.
// short : dans une carte ou une ligne étroite, « Ré-enrôler avec ce droit »,
// le droit en entier dans l'infobulle et pour les lecteurs d'écran.
export function MissingRightButton({ machineID, right, path, short = false }: { machineID: string; right: string; path?: string | undefined; short?: boolean }) {
  const t = useT();
  const full = t("machine.reenroll_with", t(capabilityLabelKey(right)));
  return (
    <Button small icon="refresh" to={reenrollPath(machineID, { name: right, path })} {...(short ? { title: full, "aria-label": full } : {})}>
      {short ? t("machine.reenroll_with_right") : full}
    </Button>
  );
}

// Une note qui dit qu'un droit manque (children) et porte son geste.
export function MissingRight({ machineID, right, path, children }: { machineID: string; right: string; path?: string | undefined; children: ReactNode }) {
  return (
    <Note tone="warn">
      <p>{children}</p>
      <MissingRightButton machineID={machineID} right={right} path={path} />
    </Note>
  );
}
