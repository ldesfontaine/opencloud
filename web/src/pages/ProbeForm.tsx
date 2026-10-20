import { useEffect, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { useContainer } from "../api/containers";
import { useDeclaredServices } from "../api/directory";
import { useMachines } from "../api/machines";
import { saveProbe, useProbe, type ProbeResponse } from "../api/probes";
import { Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { EnumSelect, Field, Input, Select } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { MachineSelect } from "../components/MachineSelect";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { emptyProbeForm, probeFormOf, probeKinds, probeRequestOf, probeSuggestionOf, type ProbeFormValues } from "../lib/probes";
import { NotFound } from "./NotFound";

// Créer ou modifier une sonde : l'opérateur dit ce qu'il faut joindre, tous
// les combien, et depuis quelle machine ; le serveur valide et répond par la
// clé du refus. La sonde vise un service déclaré de l'annuaire, sur n'importe
// quelle machine : venu de sa fiche (?service=), ou d'un conteneur
// (?service=&container=), le formulaire de création arrive déjà rempli. En
// édition, il part de la sonde et la remplace entière ; ce qui change ce
// qu'elle mesure la remet à « Nouveau ».
export function ProbeForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const fromService = params.get("service") ?? "";
  const fromContainer = params.get("container") ?? "";
  const machines = useMachines();
  const declared = useDeclaredServices();
  const container = useContainer(fromContainer === "" ? null : fromContainer);
  const existing = useProbe(id ?? null);
  const form = useForm(
    { ...emptyProbeForm, serviceID: fromService },
    isEditing ? { source: existing.data, fill: (response: ProbeResponse) => probeFormOf(response.probe) } : null,
  );
  const { values, change, update } = form;
  const action = useAction();
  const [prefilled, setPrefilled] = useState(false);

  // À défaut de choix, la machine openCloud sonde : c'est la vue depuis
  // l'extérieur, celle qu'on veut presque toujours.
  const local = machines.data?.machines.find((machine) => machine.local);
  useEffect(() => {
    if (!isEditing && values.machineID === "" && local !== undefined) {
      change("machineID", local.id);
    }
  }, [isEditing, local, values.machineID]);

  // Venue d'un service déclaré, ou d'un de ses conteneurs, la sonde part de
  // ce qu'ils disent (probeSuggestionOf), une seule fois, quand tout est lu.
  const service = declared.data?.services.find((candidate) => candidate.id === fromService);
  const carrier = container.data?.container;
  const ready = service !== undefined && (fromContainer === "" || carrier !== undefined);
  useEffect(() => {
    if (prefilled || fromService === "" || !ready) {
      return;
    }
    setPrefilled(true);
    update(probeSuggestionOf(service, carrier));
  }, [fromService, ready, service, carrier, prefilled]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const save = () => {
    const body = probeRequestOf(values);
    action.run(() => saveProbe(id, body), {
      topic: "all",
      onDone: (saved) => void navigate(`/domaines/${saved.id}`),
    });
  };

  return (
    <>
      <PageHead
        title={isEditing ? t("probe.edit_title") : t("probe.new_title")}
        subtitle={isEditing ? t("probe.edit_subtitle") : t("probe.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={isEditing ? t("probe.save") : t("probe.create")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("probe.cancel")}
            cancelTo={isEditing ? `/domaines/${id}` : "/domaines"}
          />
        }
      >
        <Field label={t("probe.name_label")} htmlFor="probe-name" help={t("probe.name_help")}>
          <Input
            id="probe-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("probe.name_placeholder")}
            maxLength={80}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <div className="grid-2">
          <Field label={t("probe.kind_label")} htmlFor="probe-kind">
            <EnumSelect id="probe-kind" value={values.kind} values={probeKinds} labelKey="probe.kind_" onChange={(kind) => change("kind", kind)} />
          </Field>
          <Field label={t("probe.machine_label")} htmlFor="probe-machine" help={t("probe.machine_help")}>
            <MachineSelect
              id="probe-machine"
              icon="server"
              machines={machines.data?.machines ?? []}
              value={values.machineID}
              onChange={(machineID) => change("machineID", machineID)}
            />
          </Field>
        </div>
        <Field label={t("probe.service_label")} htmlFor="probe-service" help={t("probe.service_help")}>
          <Select id="probe-service" value={values.serviceID} onChange={(event) => change("serviceID", event.target.value)}>
            <option value="">{t("probe.service_none")}</option>
            {(declared.data?.services ?? []).map((candidate) => (
              <option key={candidate.id} value={candidate.id}>
                {t("probe.service_option", candidate.title !== "" ? candidate.title : candidate.name, candidate.machine_name)}
              </option>
            ))}
          </Select>
        </Field>
        <Field
          label={t("probe.target_label")}
          htmlFor="probe-target"
          help={t(values.kind === "http" ? "probe.target_help_http" : "probe.target_help_tcp")}
        >
          <Input
            id="probe-target"
            mono
            type="text"
            value={values.target}
            onChange={(event) => change("target", event.target.value)}
            placeholder={values.kind === "http" ? t("probe.target_placeholder_http") : t("probe.target_placeholder_tcp")}
            maxLength={512}
            autoComplete="off"
            required
          />
        </Field>
        <div className="grid-2">
          <Field label={t("probe.interval_label")} htmlFor="probe-interval" help={t("probe.interval_help")}>
            <Input id="probe-interval" mono type="number" value={values.interval} onChange={(event) => change("interval", event.target.value)} min={30} max={86400} required />
          </Field>
          <Field label={t("probe.timeout_label")} htmlFor="probe-timeout" help={t("probe.timeout_help")}>
            <Input id="probe-timeout" mono type="number" value={values.timeout} onChange={(event) => change("timeout", event.target.value)} min={1} max={30} required />
          </Field>
        </div>
        <div className="grid-2">
          <Field label={t("probe.failure_label")} htmlFor="probe-failure" help={t("probe.failure_help")}>
            <Input id="probe-failure" mono type="number" value={values.failure} onChange={(event) => change("failure", event.target.value)} min={1} max={10} required />
          </Field>
          <Field label={t("probe.recovery_label")} htmlFor="probe-recovery" help={t("probe.recovery_help")}>
            <Input id="probe-recovery" mono type="number" value={values.recovery} onChange={(event) => change("recovery", event.target.value)} min={1} max={10} required />
          </Field>
        </div>
        {values.kind === "http" && (
          <>
            <div className="grid-2">
              <Field label={t("probe.method_label")} htmlFor="probe-method">
                <Select id="probe-method" value={values.method} onChange={(event) => change("method", event.target.value)}>
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                  <option value="POST">POST</option>
                </Select>
              </Field>
              <Field label={t("probe.expected_status_label")} htmlFor="probe-status" help={t("probe.expected_status_help")}>
                <Input id="probe-status" mono type="text" value={values.expectedStatus} onChange={(event) => change("expectedStatus", event.target.value)} maxLength={40} autoComplete="off" />
              </Field>
            </div>
            <div className="grid-2">
              <Field label={t("probe.expected_body_label")} htmlFor="probe-body" help={t("probe.expected_body_help")}>
                <Input id="probe-body" type="text" value={values.expectedBody} onChange={(event) => change("expectedBody", event.target.value)} maxLength={200} autoComplete="off" />
              </Field>
              <Field label={t("probe.follow_redirects_label")} htmlFor="probe-redirects">
                <Select id="probe-redirects" value={values.redirects} onChange={(event) => change("redirects", event.target.value as ProbeFormValues["redirects"])}>
                  <option value="yes">{t("probe.on")}</option>
                  <option value="no">{t("probe.off")}</option>
                </Select>
              </Field>
            </div>
          </>
        )}
        {values.kind === "tcp" && (
          <Field label={t("probe.tls_label")} htmlFor="probe-tls" help={t("probe.tls_help")}>
            <Select id="probe-tls" value={values.useTLS} onChange={(event) => change("useTLS", event.target.value as ProbeFormValues["useTLS"])}>
              <option value="no">{t("probe.off")}</option>
              <option value="yes">{t("probe.on")}</option>
            </Select>
          </Field>
        )}
        {isEditing && <Note tone="warn">{t("probe.edit_reset_note")}</Note>}
      </FormCard>
    </>
  );
}
