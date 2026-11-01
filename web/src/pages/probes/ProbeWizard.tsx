import { useEffect, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { useContainer } from "../../api/containers";
import { useDeclaredServices } from "../../api/directory";
import { useMachines, type Machine } from "../../api/machines";
import { saveProbe, useProbe, type ProbeResponse } from "../../api/probes";
import { Note } from "../../components/Card";
import { Fold } from "../../components/Disclosure";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { MachineSelect } from "../../components/MachineSelect";
import { Modal } from "../../components/Modal";
import { EnumSelect, Select } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { isFormModified } from "../../lib/modal";
import { emptyProbeForm, probeFormOf, probeKinds, probeListPath, probePath, probeRequestOf, probeSuggestionOf, type ProbeFormValues } from "../../lib/probes";

// « Ajouter une sonde » : la fenêtre de la liste des sondes, à l'adresse
// /disponibilite/nouvelle, ou de la fiche d'une sonde, à
// /disponibilite/:id/modifier. Deux étapes : la cible (ce qu'il faut joindre,
// depuis quelle machine), puis le rythme (tous les combien, après combien
// d'échecs) ; ce qui est propre à HTTP ou à TCP est sous « Avancé ». La sonde
// vise un service déclaré de l'annuaire, sur n'importe quelle machine : venue
// de sa fiche (?service=), ou d'un conteneur (?service=&container=),
// l'assistant arrive déjà rempli. En édition, il s'ouvre sur le récapitulatif
// et remplace la sonde entière ; ce qui change ce qu'elle mesure la remet à
// « Nouveau ». Le serveur valide et répond par la clé du refus.
export function ProbeWizard() {
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

  // À défaut de choix, la première machine de la liste : le sélecteur n'a
  // pas d'option vide, il montrerait une machine sans l'avoir choisie.
  const first = machines.data?.machines[0];
  useEffect(() => {
    if (!isEditing && values.machineID === "" && first !== undefined) {
      change("machineID", first.id);
    }
  }, [isEditing, first, values.machineID]);

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

  const close = () => void navigate(isEditing ? probePath(id) : probeListPath, { replace: true });
  const save = () => {
    const body = probeRequestOf(values);
    action.run(() => saveProbe(id, body), {
      topic: "all",
      onDone: (saved) => void navigate(probePath(saved.id), { replace: true }),
    });
  };

  // Ce dont part la saisie : ce que le serveur garde, ou la sonde neuve avec
  // ce qui la pré-remplit. Seul ce que l'opérateur a changé demande de confirmer l'abandon.
  const suggestion = ready ? probeSuggestionOf(service, carrier) : {};
  const base: ProbeFormValues = isEditing && existing.data !== null ? probeFormOf(existing.data.probe) : { ...emptyProbeForm, serviceID: fromService, machineID: first?.id ?? "", ...suggestion };
  return (
    <Modal title={isEditing ? t("probe.edit_title") : t("probe.new_title")} onClose={close} dirty={form.isFilled && isFormModified(values, base)} wide>
      <p className="secondary">{isEditing ? t("probe.edit_subtitle") : t("probe.new_subtitle")}</p>
      {isEditing && <Note tone="warn">{t("probe.edit_reset_note")}</Note>}
      {existing.error && <Failure error={existing.error} />}
      {form.isFilled && (
        <Wizard
          steps={stepsOf(t, form, machines.data?.machines ?? [], declared.data?.services ?? [])}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("probe.save") : t("probe.create")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
        />
      )}
    </Modal>
  );
}

type ProbeForm = ReturnType<typeof useForm<ProbeFormValues, ProbeResponse>>;

function stepsOf(
  t: Translate,
  form: ProbeForm,
  machines: Pick<Machine, "id" | "name" | "kind">[],
  services: { id: string; name: string; title: string; machine_name: string }[],
): WizardStep[] {
  const { values, change } = form;
  const isHTTP = values.kind === "http";
  const service = services.find((candidate) => candidate.id === values.serviceID);
  return [
    {
      id: "target",
      title: t("probe.step_target"),
      render: () => (
        <>
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
              <MachineSelect id="probe-machine" icon="server" machines={machines} value={values.machineID} onChange={(machineID) => change("machineID", machineID)} />
            </Field>
          </div>
          <Field label={t("probe.service_label")} htmlFor="probe-service" help={t("probe.service_help")}>
            <Select
              id="probe-service"
              value={values.serviceID}
              options={[
                { value: "", label: t("probe.service_none") },
                ...services.map((candidate) => ({
                  value: candidate.id,
                  label: t("probe.service_option", candidate.title !== "" ? candidate.title : candidate.name, candidate.machine_name),
                })),
              ]}
              onChange={(chosen) => change("serviceID", chosen)}
            />
          </Field>
          <Field label={t("probe.target_label")} htmlFor="probe-target" help={t(isHTTP ? "probe.target_help_http" : "probe.target_help_tcp")}>
            <Input
              id="probe-target"
              mono
              type="text"
              value={values.target}
              onChange={(event) => change("target", event.target.value)}
              placeholder={isHTTP ? t("probe.target_placeholder_http") : t("probe.target_placeholder_tcp")}
              maxLength={512}
              autoComplete="off"
              required
            />
          </Field>
        </>
      ),
      summary: () => [
        { label: t("probe.name_label"), value: values.name },
        { label: t("probe.kind_label"), value: t(`probe.kind_${values.kind}`) },
        { label: t("probe.machine_label"), value: machines.find((machine) => machine.id === values.machineID)?.name ?? "—" },
        { label: t("probe.service_label"), value: service === undefined ? t("probe.service_none") : service.title !== "" ? service.title : service.name },
        { label: t("probe.target_label"), value: values.target, mono: true },
      ],
    },
    {
      id: "rhythm",
      title: t("probe.step_rhythm"),
      render: () => (
        <>
          <div className="grid-2">
            <Field label={t("probe.interval_label")} htmlFor="probe-interval" help={t("probe.interval_help")}>
              <Input id="probe-interval" mono type="number" value={values.interval} onChange={(event) => change("interval", event.target.value)} min={30} max={86400} required autoFocus />
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
          <Fold label={t("wizard.advanced")} initiallyOpen={false}>
            {isHTTP ? <HTTPFields form={form} /> : <TCPFields form={form} />}
          </Fold>
        </>
      ),
      summary: () => [
        { label: t("probe.interval_label"), value: values.interval, mono: true },
        { label: t("probe.timeout_label"), value: values.timeout, mono: true },
        { label: t("probe.failure_label"), value: values.failure, mono: true },
        { label: t("probe.recovery_label"), value: values.recovery, mono: true },
        ...(isHTTP
          ? [
              { label: t("probe.method_label"), value: values.method, mono: true },
              { label: t("probe.expected_status_label"), value: values.expectedStatus === "" ? "—" : values.expectedStatus, mono: true },
              { label: t("probe.expected_body_label"), value: values.expectedBody === "" ? "—" : values.expectedBody },
              { label: t("probe.follow_redirects_label"), value: t(values.redirects === "yes" ? "probe.on" : "probe.off") },
            ]
          : [{ label: t("probe.tls_label"), value: t(values.useTLS === "yes" ? "probe.on" : "probe.off") }]),
      ],
    },
  ];
}

// Ce qui n'a de sens que pour une sonde HTTP : la requête et la réponse attendue.
function HTTPFields({ form }: { form: ProbeForm }) {
  const t = useT();
  const { values, change } = form;
  return (
    <>
      <div className="grid-2">
        <Field label={t("probe.method_label")} htmlFor="probe-method">
          <Select
            id="probe-method"
            value={values.method}
            options={[
              { value: "GET", label: "GET" },
              { value: "HEAD", label: "HEAD" },
              { value: "POST", label: "POST" },
            ]}
            onChange={(chosen) => change("method", chosen)}
          />
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
          <Select
            id="probe-redirects"
            value={values.redirects}
            options={[
              { value: "yes", label: t("probe.on") },
              { value: "no", label: t("probe.off") },
            ]}
            onChange={(chosen) => change("redirects", chosen as ProbeFormValues["redirects"])}
          />
        </Field>
      </div>
    </>
  );
}

// Une sonde TCP peut faire une poignée de main TLS plutôt qu'une simple connexion.
function TCPFields({ form }: { form: ProbeForm }) {
  const t = useT();
  const { values, change } = form;
  return (
    <Field label={t("probe.tls_label")} htmlFor="probe-tls" help={t("probe.tls_help")}>
      <Select
        id="probe-tls"
        value={values.useTLS}
        options={[
          { value: "no", label: t("probe.off") },
          { value: "yes", label: t("probe.on") },
        ]}
        onChange={(chosen) => change("useTLS", chosen as ProbeFormValues["useTLS"])}
      />
    </Field>
  );
}
