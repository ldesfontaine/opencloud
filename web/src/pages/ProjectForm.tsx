import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { deleteProject, fetchProjectDeletePlan, saveProject, useProject, useProjectServices, type Project } from "../api/directory";
import { Card, CardBody, CardHeader, Empty } from "../components/Card";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Failure } from "../components/Failure";
import { Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT, type Translate } from "../i18n/context";
import { emptyProjectForm, projectFormOf } from "../lib/directory";
import { NotFound } from "./NotFound";

// Créer ou modifier un projet : un nom et un titre. L'appartenance des
// services se règle depuis le service ; en édition, la page liste ceux que
// le projet regroupe, et le supprimer ne supprime pas leurs déclarations.
export function ProjectForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const existing = useProject(id ?? null);
  const members = useProjectServices(id ?? null);
  const form = useForm(emptyProjectForm, isEditing ? { source: existing.data, fill: projectFormOf } : null);
  const { values, change } = form;
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const save = () => {
    action.run(() => saveProject(id, values), {
      topic: "directory",
      onDone: () => void navigate("/services/projets"),
    });
  };

  const projectServices = members.data?.services ?? [];
  return (
    <>
      <PageHead
        title={isEditing ? t("project.edit_title") : t("project.new_title")}
        subtitle={isEditing ? t("project.edit_subtitle") : t("project.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={isEditing ? t("project.save") : t("project.create")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            canSubmit={form.isFilled}
            cancelLabel={t("project.cancel")}
            cancelTo="/services/projets"
            deleteLabel={t("project.delete")}
            onDelete={isEditing && existing.data !== null ? () => setRemoving(true) : undefined}
          />
        }
      >
        <Field label={t("project.name_label")} htmlFor="project-name" help={t("project.name_help")}>
          <Input
            id="project-name"
            mono
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("project.name_placeholder")}
            maxLength={63}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("project.title_label")} htmlFor="project-title" help={t("project.title_help")}>
          <Input id="project-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={80} autoComplete="off" />
        </Field>
      </FormCard>
      {removing && isEditing && existing.data !== null && (
        <DeletePlanDialog
          question={deleteConfirm(t, existing.data)}
          confirmLabel={t("project.delete")}
          loadPlan={() => fetchProjectDeletePlan(id)}
          perform={() => deleteProject(id)}
          topic="directory"
          onDone={() => void navigate("/services/projets")}
          onClose={() => setRemoving(false)}
        />
      )}
      {isEditing && (
        <Card>
          <CardHeader title={t("project.services_title")} aside={t("project.services_text")} />
          {projectServices.length > 0 ? (
            <CardBody gap={8}>
              {projectServices.map((service) => (
                <Link key={service.id} to={`/services/annuaire/${service.id}`}>
                  {service.name}
                  <span className="muted"> · {service.machine_name}</span>
                </Link>
              ))}
            </CardBody>
          ) : (
            <Empty text={t("project.services_empty")} />
          )}
        </Card>
      )}
    </>
  );
}

// Ce que la suppression coûte, dit avant de la faire : les services
// quittent le projet, ils restent dans l'annuaire.
function deleteConfirm(t: Translate, project: Project): string {
  switch (project.service_ids.length) {
    case 0:
      return t("project.delete_confirm_none", project.name);
    case 1:
      return t("project.delete_confirm_one", project.name);
    default:
      return t("project.delete_confirm", project.service_ids.length, project.name);
  }
}
