import { useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";

import { useProjects, type Project } from "../api/directory";
import { deleteStatusComponent, fetchComponentDeletePlan, saveStatusComponent, useStatusComponent, type StatusComponent, type StatusMember } from "../api/status";
import { Button } from "../components/Button";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Loaded } from "../components/Loaded";
import { Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { ObjectPicker } from "../components/ObjectPicker";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Select } from "../components/Select";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { chosenProjectID, projectLabel } from "../lib/projects";
import { componentFormOf, componentRequestOf, emptyComponentForm, publicPagePath } from "../lib/status";
import { StatusStatePill } from "./availability/StatusStatePill";

// Créer ou modifier un composant : son projet (à la création), un nom public,
// un ordre, et les objets rattachés, choisis parmi ce qu'openCloud surveille
// déjà. Une création part du projet de ?project=, qui doit être publié : un
// composant n'a de sens que sur une page.
//
// Les projets lus ici passent au formulaire : une seconde lecture, dans le
// formulaire, partait vide et le menu Projet montrait un autre projet que
// celui du formulaire.
export function StatusComponentForm() {
  const { id } = useParams();
  const [params] = useSearchParams();
  const component = useStatusComponent(id ?? null);
  const projects = useProjects();
  if (id === undefined) {
    return (
      <Loaded resource={projects}>
        {(data) => {
          const published = data.projects.filter((project) => project.publication !== null);
          return <ComponentForm existing={null} projectID={chosenProjectID(published, params.get("project")) ?? ""} projects={published} />;
        }}
      </Loaded>
    );
  }
  return <Loaded resource={component}>{(data) => <ComponentForm existing={data} projectID={data.project_id} projects={projects.data?.projects ?? []} />}</Loaded>;
}

export function ComponentForm({ existing, projectID, projects }: { existing: StatusComponent | null; projectID: string; projects: readonly Project[] }) {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm(existing === null ? emptyComponentForm(projectID) : componentFormOf(existing));
  // Le retour mène à la page du projet du formulaire.
  const back = publicPagePath(form.values.projectID);
  const { values, change } = form;
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  const known = new Map<string, StatusMember>((existing?.members ?? []).map((member) => [`${member.kind}:${member.id}`, member]));

  const save = () => {
    const body = componentRequestOf(values);
    action.run(() => saveStatusComponent(existing?.id, body), {
      topic: "status",
      onDone: () => void navigate(back),
    });
  };

  return (
    <FormPage>
      <PageHead
        title={existing ? t("component.edit_title") : t("component.new_title")}
        subtitle={t("component.new_subtitle")}
        actions={
          existing && (
            <Button variant="danger" icon="x" onClick={() => setRemoving(true)}>
              {t("component.delete")}
            </Button>
          )
        }
      />
      {removing && existing && (
        <DeletePlanDialog
          question={t("component.delete_confirm", existing.name)}
          confirmLabel={t("component.delete")}
          loadPlan={() => fetchComponentDeletePlan(existing.id)}
          perform={() => deleteStatusComponent(existing.id)}
          topic="status"
          onDone={() => void navigate(back)}
          onClose={() => setRemoving(false)}
        />
      )}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={existing ? t("component.save") : t("component.create")}
            submitIcon={existing ? "check" : "plus"}
            busy={action.busy}
            cancelLabel={t("component.cancel")}
            cancelTo={back}
          />
        }
      >
        <Field label={t("component.project_label")} htmlFor="component-project" help={t("component.project_help")}>
          <Select
            id="component-project"
            value={values.projectID}
            options={projects.map((project) => ({ value: project.id, label: projectLabel(project) }))}
            onChange={(chosen) => change("projectID", chosen)}
            disabled={existing !== null}
          />
        </Field>
        <Field label={t("component.name_label")} htmlFor="component-name" help={t("component.name_help")}>
          <Input
            id="component-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("component.name_placeholder")}
            maxLength={80}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("component.position_label")} htmlFor="component-position" help={t("component.position_help")}>
          <Input id="component-position" mono type="number" value={values.position} onChange={(event) => change("position", event.target.value)} min={0} max={1000} />
        </Field>
        <Field label={t("component.objects_label")} help={t("component.objects_help")}>
          <ObjectPicker
            value={values.members}
            onChange={(members) => change("members", members)}
            labelKeyPrefix="component.kind_"
            label={t("component.objects_label")}
            addLabel={t("component.add_object")}
            emptyText={t("component.objects_empty")}
            nameOf={(choice) => known.get(`${choice.kind}:${choice.id}`)?.name ?? t("component.missing")}
            detailOf={(choice) => {
              const detail = known.get(`${choice.kind}:${choice.id}`);
              return detail ? <MemberPill member={detail} /> : null;
            }}
          />
        </Field>
      </FormCard>
    </FormPage>
  );
}

// Ce qu'un objet apporte : sa pastille, ou pourquoi il ne compte pas.
function MemberPill({ member }: { member: StatusMember }) {
  const t = useT();
  if (!member.present) {
    return <Pill tone="danger">{t("component.missing")}</Pill>;
  }
  if (!member.counts) {
    return <Pill tone="neutral">{t("component.counts_no")}</Pill>;
  }
  return <StatusStatePill state={member.state} />;
}
