import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { api, errorKey, segment } from "../api/client";
import type { DeclaredServicesResponse, Project } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT, type Translate } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { NotFound } from "./NotFound";

// Créer ou modifier un projet : un nom et un titre. L'appartenance des
// services se règle depuis le service ; en édition, la page liste ceux que
// le projet regroupe, et le supprimer ne supprime pas leurs déclarations.
export function ProjectForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const existing = useResource<Project>(isEditing ? () => `/api/directory/projects/${segment(id)}` : null);
  const members = useResource<DeclaredServicesResponse>(isEditing ? () => `/api/directory/services?project=${segment(id)}` : null);
  const [name, setName] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Le projet se lit une fois : une relecture ne doit pas écraser ce que
  // l'opérateur est en train de taper.
  const [filled, setFilled] = useState(false);

  useEffect(() => {
    if (filled || existing.data === null) {
      return;
    }
    setFilled(true);
    setName(existing.data.name);
    setTitle(existing.data.title);
  }, [existing.data, filled]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const body = { name, title };
    const request = isEditing ? api.put<Project>(`/api/directory/projects/${segment(id)}`, body) : api.post<Project>("/api/directory/projects", body);
    request
      .then(() => {
        refresh("directory");
        void navigate("/services/projets");
      })
      .catch((failed: unknown) => setError(errorKey(failed)))
      .finally(() => setBusy(false));
  };

  const remove = () => {
    if (!isEditing || !existing.data || !window.confirm(deleteConfirm(t, existing.data))) {
      return;
    }
    api
      .delete(`/api/directory/projects/${segment(id)}`)
      .then(() => {
        refresh("directory");
        void navigate("/services/projets");
      })
      .catch((failed: unknown) => setError(errorKey(failed)));
  };

  const projectServices = members.data?.services ?? [];
  return (
    <>
      <PageHead
        title={isEditing ? t("project.edit_title") : t("project.new_title")}
        subtitle={isEditing ? t("project.edit_subtitle") : t("project.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("project.name_label")} htmlFor="project-name" help={t("project.name_help")}>
            <Input
              id="project-name"
              mono
              type="text"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t("project.name_placeholder")}
              maxLength={63}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <Field label={t("project.title_label")} htmlFor="project-title" help={t("project.title_help")}>
            <Input id="project-title" type="text" value={title} onChange={(event) => setTitle(event.target.value)} maxLength={80} autoComplete="off" />
          </Field>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={isEditing ? "check" : "plus"} disabled={busy || (isEditing && !filled)}>
              {isEditing ? t("project.save") : t("project.create")}
            </Button>
            <Button variant="ghost" to="/services/projets">
              {t("project.cancel")}
            </Button>
            {isEditing && (
              <Button variant="danger" icon="x" onClick={remove} disabled={busy}>
                {t("project.delete")}
              </Button>
            )}
          </div>
        </form>
      </Card>
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
