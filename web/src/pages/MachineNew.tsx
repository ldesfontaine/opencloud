import { useNavigate } from "react-router";

import { createMachineToken } from "../api/machines";
import { Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { RightsFields } from "../components/RightsFields";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { capabilitiesOf, emptyMachineForm, tokenPageState } from "../lib/machines";

// L'opérateur nomme la machine et coche les droits de son agent ; le jeton
// naît et s'affiche une seule fois, sur la page suivante, avec la commande à
// coller, qui écrit ces droits dans le fichier de l'agent.
export function MachineNew() {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm(emptyMachineForm);
  const { values, change } = form;
  const action = useAction();

  const save = () => {
    const rights = capabilitiesOf(values);
    action.run(() => createMachineToken(values.name, rights), {
      topic: "all",
      onDone: (token) => void navigate("/machines/jeton", { state: tokenPageState(token, rights) }),
    });
  };

  return (
    <>
      <PageHead title={t("machine.new_title")} subtitle={t("machine.new_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("machine.create_token")}
            submitIcon="plus"
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("machine.cancel")}
            cancelTo="/machines"
          />
        }
      >
        <Field label={t("machine.name_label")} htmlFor="machine-name" help={t("machine.name_help")}>
          <Input
            id="machine-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("machine.name_placeholder")}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <RightsFields values={values} update={form.update} />
      </FormCard>
    </>
  );
}
