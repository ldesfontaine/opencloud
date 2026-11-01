import { Link, useNavigate, useParams } from "react-router";

import { saveProject, useProject, useProjectServices } from "../../api/directory";
import { Failure } from "../../components/Failure";
import { Checkbox, Field, Input, Textarea } from "../../components/Field";
import { Modal } from "../../components/Modal";
import { EnumSelect } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useI18n, useT, type Translate } from "../../i18n/context";
import { isFormModified } from "../../lib/modal";
import { emptyProjectForm, projectFormOf, projectRequestOf, publicAddressOf, slugSuggestionOf, type ProjectFormValues } from "../../lib/projects";

const projectsPath = "/services/projets";

// « Nouveau projet » : la fenêtre de la liste des projets, à l'adresse
// /services/projets/nouveau, ou « Modifier le projet », à
// /services/projets/:id. Deux étapes : le projet (un nom, un titre), puis sa
// page publique, facultative : un projet publié a sa page à /statut/<adresse>,
// qui montre un composant par service du projet et porte son titre. Dépublier
// retire la page, pas le projet. En édition, l'assistant s'ouvre sur le
// récapitulatif et remplace le projet entier ; le serveur valide et répond par
// la clé du refus.
export function ProjectWizard() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const { language } = useI18n();
  const navigate = useNavigate();
  const existing = useProject(id ?? null);
  const members = useProjectServices(id ?? null);
  const form = useForm(emptyProjectForm(language), isEditing ? { source: existing.data, fill: projectFormOf } : null);
  const action = useAction();

  const close = () => void navigate(projectsPath, { replace: true });
  const save = () => {
    const body = projectRequestOf(form.values);
    action.run(() => saveProject(id, body), { topic: "all", onDone: close });
  };

  const base = isEditing && existing.data !== null ? projectFormOf(existing.data) : emptyProjectForm(language);
  return (
    <Modal title={isEditing ? t("project.edit_title") : t("project.new_title")} onClose={close} dirty={form.isFilled && isFormModified(form.values, base)} wide>
      <p className="secondary">{isEditing ? t("project.edit_subtitle") : t("project.new_subtitle")}</p>
      {existing.error && <Failure error={existing.error} />}
      {form.isFilled && (
        <Wizard
          steps={stepsOf(t, form, members.data?.services ?? [])}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("project.save") : t("project.create")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
        />
      )}
    </Modal>
  );
}

type ProjectForm = ReturnType<typeof useForm<ProjectFormValues, Parameters<typeof projectFormOf>[0]>>;

function stepsOf(t: Translate, form: ProjectForm, services: { id: string; name: string; machine_name: string }[]): WizardStep[] {
  const { values, change } = form;
  return [
    {
      id: "project",
      title: t("project.step_project"),
      render: () => (
        <>
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
          {services.length > 0 && (
            <p className="secondary">
              {t("project.services_text")}{" "}
              {services.map((service, index) => (
                <span key={service.id}>
                  {index > 0 && ", "}
                  <Link to={`/services/annuaire/${service.id}`}>{service.name}</Link>
                </span>
              ))}
            </p>
          )}
        </>
      ),
      summary: () => [
        { label: t("project.name_label"), value: values.name, mono: true },
        { label: t("project.title_label"), value: values.title === "" ? "—" : values.title },
      ],
    },
    {
      id: "publication",
      title: t("project.step_publication"),
      render: () => (
        <>
          <Field label={t("project.publish_label")} help={t("project.publish_help")} tip={t("project.publish_tip")}>
            <Checkbox
              label={t("project.publish_check")}
              checked={values.published}
              onChange={(published) => {
                change("published", published);
                if (published && values.slug === "") {
                  change("slug", slugSuggestionOf(values.name));
                }
              }}
            />
          </Field>
          {values.published && (
            <>
              <Field label={t("project.slug_label")} htmlFor="project-slug" help={t("project.slug_help")}>
                <Input
                  id="project-slug"
                  mono
                  type="text"
                  value={values.slug}
                  onChange={(event) => change("slug", event.target.value)}
                  placeholder={t("project.slug_placeholder")}
                  maxLength={63}
                  autoComplete="off"
                  required
                />
              </Field>
              <Field label={t("project.announcement_label")} htmlFor="project-announcement" help={t("project.announcement_help")}>
                <Textarea id="project-announcement" value={values.announcement} onChange={(event) => change("announcement", event.target.value)} maxLength={500} rows={3} />
              </Field>
              <PublicationLanguage form={form} />
            </>
          )}
        </>
      ),
      validate: () => (values.published && values.slug.trim() === "" ? "project.publish_slug_invalid" : null),
      summary: () =>
        values.published
          ? [
              { label: t("project.publish_label"), value: publicAddressOf(values.slug), mono: true },
              { label: t("project.language_label"), value: t(`language.${values.language}`) },
              { label: t("project.announcement_label"), value: values.announcement === "" ? "—" : values.announcement },
            ]
          : [{ label: t("project.publish_label"), value: t("project.not_published") }],
    },
  ];
}

function PublicationLanguage({ form }: { form: ProjectForm }) {
  const t = useT();
  const { languages } = useI18n();
  return (
    <Field label={t("project.language_label")} htmlFor="project-language" help={t("project.language_help")}>
      <EnumSelect id="project-language" value={form.values.language} values={languages} labelKey="language." onChange={(chosen) => form.change("language", chosen)} />
    </Field>
  );
}
