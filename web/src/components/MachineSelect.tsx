import type { Machine } from "../api/machines";
import { useT } from "../i18n/context";
import { machineOption } from "../lib/machines";
import { Select, type SelectProps } from "./Select";

// Le choix d'une machine, par son nom. noneLabel ajoute en tête l'option
// vide et dit ce qu'elle veut dire : « aucune » dans un formulaire, « toutes »
// dans un filtre ; sans lui, il faut choisir une machine.
export function MachineSelect({
  machines,
  noneLabel,
  ...rest
}: Omit<SelectProps, "options" | "placeholder"> & {
  machines: readonly Pick<Machine, "id" | "name" | "kind">[];
  noneLabel?: string;
}) {
  const t = useT();
  const options = machines.map((machine) => machineOption(t, machine));
  if (noneLabel !== undefined) {
    options.unshift({ value: "", label: noneLabel });
  }
  return <Select options={options} {...rest} />;
}
