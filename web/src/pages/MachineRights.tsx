import { useNavigate, useParams } from "react-router";

import { reenrollMachine, useMachines, type Machine } from "../api/machines";
import { Failure } from "../components/Failure";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { RightsFields } from "../components/RightsFields";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { capabilitiesOf, rightsFormOf, tokenPageState, type RightsValues } from "../lib/machines";
import { NotFound } from "./NotFound";

const noRights: RightsValues = { rights: [], backupPath: "", writePaths: "" };

// Changer les droits d'un agent, c'est le ré-enrôler (décision 10) : un
// nouveau jeton qui porte les droits cochés, dont la ligne s'affiche une
// fois. Le formulaire part de ce que le jeton de l'agent courant demandait.
export function MachineRights() {
  const { id = "" } = useParams();
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (machines.data && !current) {
    return <NotFound />;
  }
  if (!current) {
    return machines.error ? <Failure error={machines.error} /> : null;
  }
  return <RightsForm machine={current} />;
}

function RightsForm({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm(noRights, { source: machine.agent?.requested ?? [], fill: rightsFormOf });
  const { values } = form;
  const action = useAction();
  const save = () => {
    const rights = capabilitiesOf(values);
    action.run(() => reenrollMachine(machine.id, rights), {
      topic: "all",
      onDone: (token) => void navigate("/machines/jeton", { state: tokenPageState(token, rights) }),
    });
  };
  return (
    <FormPage>
      <PageHead title={t("machine.rights_title", machine.name)} subtitle={t("machine.rights_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("machine.rights_submit")}
            submitIcon="refresh"
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("machine.cancel")}
            cancelTo={`/machines/${machine.id}`}
          />
        }
      >
        <RightsFields values={values} update={form.update} />
      </FormCard>
    </FormPage>
  );
}
