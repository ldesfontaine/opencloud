import type { ReachMachine } from "../../api/apiTokens";
import { Select } from "../../components/Select";
import { Note } from "../../components/Card";
import { Input } from "../../components/Field";
import { useT } from "../../i18n/context";
import { parseGuestNumbers, type ReachFormValues, type ReachMode } from "../../lib/apiTokens";
import "./VerbChoices.scss";

// Jusqu'où les verbes d'un jeton en écriture portent : toutes les machines ou
// une liste à cocher ; et, si un verbe Proxmox est coché, tous les invités ou
// des numéros. Rien n'est limité d'avance. Une liste vide est permise et
// dite : le jeton n'agirait sur rien.
export function ReachChoices({
  machines,
  values,
  withGuests,
  idPrefix,
  onChange,
}: {
  machines: ReachMachine[];
  values: ReachFormValues;
  withGuests: boolean;
  idPrefix: string;
  onChange: (patch: Partial<ReachFormValues>) => void;
}) {
  const t = useT();
  const toggleMachine = (id: string) =>
    onChange({ machines: values.machines.includes(id) ? values.machines.filter((other) => other !== id) : [...values.machines, id] });
  const modeOptions = (all: string, some: string) => [
    { value: "all", label: all },
    { value: "some", label: some },
  ];
  const guestsInvalid = withGuests && values.guestsMode === "some" && parseGuestNumbers(values.guests) === null;
  return (
    <div className="reach-choices">
      <span className="field-label">{t("api_token.reach_label")}</span>
      <span className="secondary">{t("api_token.reach_help")}</span>
      <Select
        id={`${idPrefix}-machines`}
        value={values.machinesMode}
        options={modeOptions(t("api_token.reach_machines_all"), t("api_token.reach_machines_some"))}
        onChange={(chosen) => onChange({ machinesMode: chosen === "some" ? "some" : ("all" satisfies ReachMode) })}
      />
      {values.machinesMode === "some" && (
        <fieldset className="verb-choices">
          {machines.length === 0 && <span className="secondary">{t("api_token.reach_machines_empty")}</span>}
          {machines.map((machine) => (
            <label key={machine.id} className="verb-choice">
              <input type="checkbox" checked={values.machines.includes(machine.id)} onChange={() => toggleMachine(machine.id)} />
              <span className="verb-choice-label">{machine.name}</span>
            </label>
          ))}
          {values.machines.length === 0 && <Note tone="warn">{t("api_token.reach_machines_none")}</Note>}
        </fieldset>
      )}
      {withGuests && (
        <>
          <Select
            id={`${idPrefix}-guests`}
            value={values.guestsMode}
            options={modeOptions(t("api_token.reach_guests_all"), t("api_token.reach_guests_some"))}
            onChange={(chosen) => onChange({ guestsMode: chosen === "some" ? "some" : ("all" satisfies ReachMode) })}
          />
          {values.guestsMode === "some" && (
            <>
              <label className="field-label" htmlFor={`${idPrefix}-guest-numbers`}>
                {t("api_token.reach_guests_label")}
              </label>
              <Input
                id={`${idPrefix}-guest-numbers`}
                type="text"
                inputMode="numeric"
                value={values.guests}
                onChange={(event) => onChange({ guests: event.target.value })}
                autoComplete="off"
                aria-invalid={guestsInvalid}
              />
              <span className="secondary">{t("api_token.reach_guests_help")}</span>
              {guestsInvalid && <Note tone="warn">{t("api_token.reach_guests_invalid")}</Note>}
              {!guestsInvalid && values.guests.trim() === "" && <Note tone="warn">{t("api_token.reach_guests_none")}</Note>}
            </>
          )}
        </>
      )}
    </div>
  );
}
