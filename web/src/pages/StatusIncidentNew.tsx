import { useNavigate } from "react-router";

import { createIncident, useStatusComponents, useStatusPages } from "../api/status";
import { Note } from "../components/Card";
import { Checkbox, Checks, EnumSelect, Field, Input, Textarea } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { componentChoiceLabel, emptyIncidentForm, incidentImpacts, incidentKinds, incidentRequestOf, openingStatuses, toggleComponent } from "../lib/status";

// Ouvrir un incident ou planifier une maintenance : un titre, un impact,
// un premier message, les composants touchés, et pour une maintenance sa
// fenêtre. Le serveur valide et répond par la clé du refus.
export function StatusIncidentNew() {
  const t = useT();
  const navigate = useNavigate();
  const components = useStatusComponents(null);
  const pages = useStatusPages().data?.pages ?? [];
  const form = useForm(emptyIncidentForm());
  const { values, change } = form;
  const action = useAction();
  const list = components.data?.components ?? [];

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
            canSubmit={values.componentIDs.length > 0}
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
        <Field label={t("incident.components_label")} help={t("incident.components_help")}>
          {list.length === 0 ? (
            <Note tone="warn">{t("statuspage.components_empty_text")}</Note>
          ) : (
            <Checks>
              {list.map((component) => (
                <Checkbox
                  key={component.id}
                  label={componentChoiceLabel(component, pages)}
                  checked={values.componentIDs.includes(component.id)}
                  onChange={() => change("componentIDs", toggleComponent(values.componentIDs, component.id))}
                />
              ))}
            </Checks>
          )}
        </Field>
      </FormCard>
    </>
  );
}
