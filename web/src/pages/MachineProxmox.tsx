import { useNavigate, useParams } from "react-router";

import { setMachineProxmox, useMachineProxmox, type ProxmoxHost } from "../api/proxmox";
import { useMachines, type Machine } from "../api/machines";
import { Failure } from "../components/Failure";
import { Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { detectedProxmox, suggestedProxmoxURL } from "../lib/proxmox";
import { NotFound } from "./NotFound";

interface ProxmoxFormValues {
  apiURL: string;
  tokenID: string;
  tokenSecret: string;
  pin: string;
}

const emptyForm: ProxmoxFormValues = { apiURL: "", tokenID: "", tokenSecret: "", pin: "" };

// Ce que le serveur garde d'un hôte déjà déclaré, pour le modifier : jamais le
// secret, qui se ressaisit.
function formOf(host: ProxmoxHost): ProxmoxFormValues {
  return { apiURL: host.api_url, tokenID: host.token_id, tokenSecret: "", pin: host.pin };
}

// Déclarer (ou modifier) l'hôte Proxmox d'une machine. Le serveur interroge
// l'API avec le jeton avant de rien garder ; un refus reste sous le formulaire.
export function MachineProxmox() {
  const { id = "" } = useParams();
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (machines.data && !current) {
    return <NotFound />;
  }
  if (!current) {
    return machines.error ? <Failure error={machines.error} /> : null;
  }
  return <ProxmoxForm machine={current} />;
}

function ProxmoxForm({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const declared = machine.proxmox.declared;
  const host = useMachineProxmox(machine.id, declared);
  // Un hôte reconnu par l'agent arrive avec l'adresse de la machine ; le jeton,
  // lui, reste à l'opérateur.
  const suggestedURL = detectedProxmox(machine) === null ? "" : suggestedProxmoxURL(machine);
  const form = useForm({ ...emptyForm, apiURL: suggestedURL }, declared ? { source: host.data, fill: formOf } : null);
  const { values } = form;
  const action = useAction();
  const save = () => {
    action.run(() => setMachineProxmox(machine.id, { api_url: values.apiURL.trim(), token_id: values.tokenID.trim(), token_secret: values.tokenSecret, pin: values.pin.trim() }), {
      topic: "machines",
      onDone: () => void navigate(`/machines/${machine.id}/hyperviseur`),
    });
  };
  return (
    <FormPage>
      <PageHead title={t("machine.proxmox_form_title", machine.name)} subtitle={t("machine.proxmox_form_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={t("machine.proxmox_submit")}
            submitIcon="check"
            busy={action.busy}
            canSubmit={form.isFilled && values.apiURL !== "" && values.tokenID !== "" && values.tokenSecret !== ""}
            cancelLabel={t("machine.cancel")}
            cancelTo={`/machines/${machine.id}`}
          />
        }
      >
        <Field label={t("machine.proxmox_url_label")} htmlFor="proxmox-url" help={t("machine.proxmox_url_help")}>
          <Input id="proxmox-url" mono value={values.apiURL} placeholder="https://pve.lan:8006" onChange={(event) => form.change("apiURL", event.target.value)} autoComplete="off" />
        </Field>
        <Field label={t("machine.proxmox_token_id_label")} htmlFor="proxmox-token-id" help={t("machine.proxmox_token_id_help")}>
          <Input id="proxmox-token-id" mono value={values.tokenID} placeholder="lecteur@pve!lecture" onChange={(event) => form.change("tokenID", event.target.value)} autoComplete="off" />
        </Field>
        <Field label={t("machine.proxmox_secret_label")} htmlFor="proxmox-secret" help={t("machine.proxmox_secret_help")}>
          <Input id="proxmox-secret" mono type="password" value={values.tokenSecret} onChange={(event) => form.change("tokenSecret", event.target.value)} autoComplete="new-password" />
        </Field>
        <Field label={t("machine.proxmox_pin_label")} htmlFor="proxmox-pin" help={t("machine.proxmox_pin_help")}>
          <Input id="proxmox-pin" mono value={values.pin} onChange={(event) => form.change("pin", event.target.value)} autoComplete="off" />
        </Field>
      </FormCard>
    </FormPage>
  );
}
