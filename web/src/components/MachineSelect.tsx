import type { SelectHTMLAttributes } from "react";

import type { Machine } from "../api/machines";
import { Select } from "./Field";
import type { IconName } from "./Icon";

// Le choix d'une machine, par son nom. noneLabel ajoute en tête l'option
// vide et dit ce qu'elle veut dire : « aucune » dans un formulaire, « toutes »
// dans un filtre ; sans lui, il faut choisir une machine.
export function MachineSelect({
  machines,
  value,
  onChange,
  noneLabel,
  ...rest
}: Omit<SelectHTMLAttributes<HTMLSelectElement>, "value" | "onChange" | "children"> & {
  machines: readonly Pick<Machine, "id" | "name">[];
  value: string;
  onChange: (id: string) => void;
  noneLabel?: string;
  icon?: IconName;
}) {
  return (
    <Select value={value} onChange={(event) => onChange(event.target.value)} {...rest}>
      {noneLabel !== undefined && <option value="">{noneLabel}</option>}
      {machines.map((machine) => (
        <option key={machine.id} value={machine.id}>
          {machine.name}
        </option>
      ))}
    </Select>
  );
}
