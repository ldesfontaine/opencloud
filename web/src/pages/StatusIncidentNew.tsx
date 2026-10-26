import { useNavigate } from "react-router";

import { createIncident } from "../api/status";
import { Field, Input, Textarea } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { ObjectPicker } from "../components/ObjectPicker";
import { PageHead } from "../components/PageHead";
import { EnumSelect } from "../components/Select";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { emptyIncidentForm, incidentImpacts, incidentKinds, incidentRequestOf, openingStatuses } from "../lib/status";

// Ouvrir un incident ou planifier une maintenance : un titre, un impact,
// un premier message, les objets touchés, et pour une maintenance sa
// fenêtre. Le serveur valide et répond par la clé du refus.
export function StatusIncidentNew() {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm(emptyIncidentForm());
  const { values, change } = form;
  const action = useAction();

  const save = () => {
    const body = incidentRequestOf(values);
    action.run(() => createIncident(body), {
      topic: "status",
      onDone: (created) => void navigate(`/page-statut/incidents/${created.id}`),
    });
  };

  return (
    <>
      <PageHead title={t("incident.new_title")} subtitle={t("incident.new_subtitle")} />
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={values.kind === "maintenance" ? t("incident.schedule") : t("incident.open")}
            submitIcon="plus"
            busy={action.busy}
            canSubmit={values.targets.length > 0}
            cancelLabel={t("incident.cancel")}
            cancelTo="/page-statut/incidents"
          />
        }
      >
        <Field label={t("incident.kind_label")} htmlFor="incident-kind">
          <EnumSelect id="incident-kind" value={values.kind} values={incidentKinds} labelKey="incident.kind_" onChange={(kind) => change("kind", kind)} />
        </Field>
        <Field label={t("incident.title_label")} htmlFor="incident-title" help={t("incident.title_help")}>
          <Input id="incident-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={120} autoComplete="off" autoFocus required />
        </Field>
        {values.kind === "incident" && (
          <div className="grid-2">
            <Field label={t("incident.impact_label")} htmlFor="incident-impact" help={t("incident.impact_help")}>
              <EnumSelect id="incident-impact" value={values.impact} values={incidentImpacts} labelKey="incident.impact_" onChange={(impact) => change("impact", impact)} />
            </Field>
            <Field label={t("incident.status_label")} htmlFor="incident-status">
              <EnumSelect id="incident-status" value={values.status} values={openingStatuses} labelKey="incident.status_" onChange={(status) => change("status", status)} />
            </Field>
          </div>
        )}
        {values.kind === "maintenance" && (
          <div className="grid-2">
            <Field label={t("incident.starts_label")} htmlFor="incident-starts" help={t("incident.window_help")}>
              <Input id="incident-starts" mono type="datetime-local" value={values.startsAt} onChange={(event) => change("startsAt", event.target.value)} required />
            </Field>
            <Field label={t("incident.ends_label")} htmlFor="incident-ends">
              <Input id="incident-ends" mono type="datetime-local" value={values.endsAt} onChange={(event) => change("endsAt", event.target.value)} required />
            </Field>
          </div>
        )}
        <Field label={t("incident.message_label")} htmlFor="incident-message" help={t("incident.message_help")}>
          <Textarea id="incident-message" value={values.message} onChange={(event) => change("message", event.target.value)} maxLength={2000} rows={3} />
        </Field>
        <Field label={t("incident.targets_label")} help={t("incident.targets_help")}>
          <ObjectPicker
            value={values.targets}
            onChange={(targets) => change("targets", targets)}
            labelKeyPrefix="component.kind_"
            label={t("incident.targets_label")}
            addLabel={t("incident.add_target")}
            nameOf={() => t("component.missing")}
          />
        </Field>
      </FormCard>
    </>
  );
}
