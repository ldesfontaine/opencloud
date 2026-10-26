import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { useProjects } from "../api/directory";
import { deleteStatusPage, fetchStatusPageDeletePlan, saveStatusPage, useStatusPage } from "../api/status";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Failure } from "../components/Failure";
import { Field, Input, Textarea } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { EnumSelect, Select } from "../components/Select";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useI18n, useT } from "../i18n/context";
import { emptyStatusPageForm, pageName, statusPageFormOf } from "../lib/status";
import { NotFound } from "./NotFound";

// Créer ou modifier une page de statut : son adresse, son titre, une
// annonce, sa langue, et le projet qu'elle montre s'il y en a un. La
// supprimer emporte ses composants : la boîte de confirmation le dit.
export function StatusPageForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const { languages, language } = useI18n();
  const navigate = useNavigate();
  const existing = useStatusPage(id ?? null);
  const projects = useProjects();
  const form = useForm({ ...emptyStatusPageForm, language }, isEditing ? { source: existing.data, fill: statusPageFormOf } : null);
  const { values, change } = form;
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const save = () => {
    action.run(() => saveStatusPage(id, values), {
      topic: "status",
      onDone: () => void navigate("/page-statut"),
    });
  };

  return (
    <FormPage>
      <PageHead title={isEditing ? t("statuspage.edit_title") : t("statuspage.new_title")} subtitle={t("statuspage.form_subtitle")} />
      {existing.error && <Failure error={existing.error} />}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={isEditing ? t("statuspage.save") : t("statuspage.create")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("statuspage.cancel")}
            cancelTo="/page-statut"
            deleteLabel={t("statuspage.delete")}
            onDelete={isEditing && existing.data !== null ? () => setRemoving(true) : undefined}
          />
        }
      >
        <Field label={t("statuspage.slug_label")} htmlFor="status-page-slug" help={t("statuspage.slug_help")}>
          <Input
            id="status-page-slug"
            mono
            type="text"
            value={values.slug}
            onChange={(event) => change("slug", event.target.value)}
            placeholder={t("statuspage.slug_placeholder")}
            maxLength={63}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("statuspage.title_label")} htmlFor="status-page-title" help={t("statuspage.title_help")}>
          <Input id="status-page-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={120} autoComplete="off" />
        </Field>
        <Field label={t("statuspage.announcement_label")} htmlFor="status-page-announcement" help={t("statuspage.announcement_help")}>
          <Textarea id="status-page-announcement" value={values.announcement} onChange={(event) => change("announcement", event.target.value)} maxLength={500} rows={3} />
        </Field>
        <div className="grid-2">
          <Field label={t("statuspage.language_label")} htmlFor="status-page-language" help={t("statuspage.language_help")}>
            <EnumSelect id="status-page-language" value={values.language} values={languages} labelKey="language." onChange={(chosen) => change("language", chosen)} />
          </Field>
          <Field label={t("statuspage.project_label")} htmlFor="status-page-project" help={t("statuspage.project_help")}>
            <Select
              id="status-page-project"
              value={values.project_id}
              options={[
                { value: "", label: t("statuspage.project_none") },
                ...(projects.data?.projects ?? []).map((project) => ({ value: project.id, label: project.title !== "" ? project.title : project.name })),
              ]}
              onChange={(chosen) => change("project_id", chosen)}
            />
          </Field>
        </div>
      </FormCard>
      {removing && isEditing && existing.data !== null && (
        <DeletePlanDialog
          question={t("statuspage.delete_confirm", pageName(existing.data))}
          confirmLabel={t("statuspage.delete")}
          loadPlan={() => fetchStatusPageDeletePlan(id)}
          perform={() => deleteStatusPage(id)}
          topic="status"
          onDone={() => void navigate("/page-statut")}
          onClose={() => setRemoving(false)}
        />
      )}
    </FormPage>
  );
}
