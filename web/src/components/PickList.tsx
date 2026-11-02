import type { ReactNode } from "react";

import { Button } from "./Button";
import { Icon, type IconName } from "./Icon";
import { MissingRightButton } from "./MissingRight";
import "./PickList.scss";

// Un choix parmi des objets riches (une VM, une cible) : une carte par objet,
// un bouton radio, son nom et une ligne. Un objet qui ne convient pas reste
// dans la liste, grisé, avec sa raison et le geste qui y remédie : le
// ré-enrôlement avec le droit qui manque, ou l'écran où aller. Il ne se
// choisit pas, mais on voit pourquoi il manque au lieu de le chercher.
export interface PickOption {
  value: string;
  icon: IconName;
  name: string;
  detail: ReactNode;
  // Ce qui se lit à droite : une pastille, une jauge.
  end?: ReactNode;
  unfit?: {
    reason: string;
    right?: { machineID: string; name: string; path?: string } | undefined;
    link?: { to: string; label: string } | undefined;
  } | null;
}

export function PickList({ name, label, options, value, onChange, empty }: { name: string; label: string; options: readonly PickOption[]; value: string; onChange: (value: string) => void; empty?: string }) {
  if (options.length === 0 && empty !== undefined) {
    return <p className="pick-empty secondary">{empty}</p>;
  }
  return (
    <div className="pick-list" role="radiogroup" aria-label={label}>
      {options.map((option) => (
        <PickRow key={option.value} name={name} option={option} checked={option.value === value} onChange={onChange} />
      ))}
    </div>
  );
}

function PickRow({ name, option, checked, onChange }: { name: string; option: PickOption; checked: boolean; onChange: (value: string) => void }) {
  const unfit = option.unfit ?? null;
  const id = `${name}-${option.value}`;
  return (
    <div className={unfit === null ? (checked ? "pick-row pick-row-on" : "pick-row") : "pick-row pick-row-off"}>
      <input id={id} type="radio" name={name} value={option.value} checked={checked} disabled={unfit !== null} onChange={() => onChange(option.value)} aria-describedby={`${id}-detail`} />
      <span className="pick-badge" aria-hidden="true">
        <Icon name={option.icon} />
      </span>
      <label className="pick-who" htmlFor={id}>
        <b>{option.name}</b>
        <span id={`${id}-detail`}>{unfit === null ? option.detail : unfit.reason}</span>
      </label>
      <span className="pick-end">
        {unfit === null && option.end}
        {unfit?.right !== undefined && <MissingRightButton machineID={unfit.right.machineID} right={unfit.right.name} path={unfit.right.path} short />}
        {unfit?.right === undefined && unfit?.link !== undefined && (
          <Button small to={unfit.link.to}>
            {unfit.link.label}
          </Button>
        )}
      </span>
    </div>
  );
}
