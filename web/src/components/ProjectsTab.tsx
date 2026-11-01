import { useState } from "react";
import { Link, Outlet } from "react-router";

import { deleteProject, fetchProjectDeletePlan, useProjects, type Project } from "../api/directory";
import { useT, type Translate } from "../i18n/context";
import { projectsSubtitle, publicAddressOf } from "../lib/projects";
import { publicPagePath } from "../lib/status";
import { ActionButton } from "./ActionButton";
import { Card, EmptyCard } from "./Card";
import { DeletePlanDialog } from "./DeletePlanDialog";
import { Failure } from "./Failure";
import { PageHead } from "./PageHead";
import { Pill } from "./Pill";
import { ServicesTabs } from "./ServicesTabs";
import { RowLink, Table } from "./Table";
import "./ServiceTable.scss";

// L'onglet Projets : les groupes sous lesquels l'opérateur range ses
// services déclarés. Un projet peut avoir sa page publique, qui se lit sous
// Disponibilité.
export function ProjectsTab() {
  const t = useT();
  const projects = useProjects();
  const [removing, setRemoving] = useState<Project | null>(null);
  const all = projects.data?.projects ?? [];
  return (
    <>
      <PageHead
        title={t("nav.services")}
        subtitle={projects.data ? projectsSubtitle(t, all.length) : ""}
        actions={
          <ActionButton route="POST /api/directory/projects" icon="plus" to="/services/projets/nouveau">
            {t("project.add")}
          </ActionButton>
        }
      />
      <ServicesTabs />
      {projects.error && <Failure error={projects.error} />}
      {projects.data && all.length === 0 && <EmptyCard icon="archive" title={t("project.empty_title")} text={t("project.empty_text")} />}
      {projects.data && all.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("project.col_project")}</th>
                <th className="wide-only">{t("project.col_title")}</th>
                <th>{t("project.col_publication")}</th>
                <th className="th-num th-end">{t("project.col_services")}</th>
              </tr>
            </thead>
            <tbody>
              {all.map((project) => (
                <tr key={project.id}>
                  <td>
                    <RowLink to={`/services/projets/${project.id}`} icon="archive" actions={[{ label: t("project.delete"), danger: true, onSelect: () => setRemoving(project) }]}>
                      {project.name}
                    </RowLink>
                  </td>
                  <td className="wide-only">{project.title !== "" ? project.title : <span className="muted">—</span>}</td>
                  <td>
                    <PublicationCell project={project} />
                  </td>
                  <td className="num td-num td-end">{project.service_ids.length}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {removing !== null && (
        <DeletePlanDialog
          question={deleteConfirm(t, removing)}
          confirmLabel={t("project.delete")}
          loadPlan={() => fetchProjectDeletePlan(removing.id)}
          perform={() => deleteProject(removing.id)}
          topic="all"
          onDone={() => setRemoving(null)}
          onClose={() => setRemoving(null)}
        />
      )}
      {/* Les fenêtres de l'adresse : créer ou modifier un projet. */}
      <Outlet />
    </>
  );
}

// La page publique du projet : son adresse, qui mène à ses composants.
function PublicationCell({ project }: { project: Project }) {
  const t = useT();
  if (project.publication === null) {
    return <span className="muted">—</span>;
  }
  return (
    <Link to={publicPagePath(project.id)} title={t("project.open_availability")}>
      <Pill tone="ok">{publicAddressOf(project.publication.slug)}</Pill>
    </Link>
  );
}

// Ce que la suppression coûte, dit avant de la faire : les services quittent
// le projet, ils restent dans l'annuaire ; sa page publique part avec lui.
function deleteConfirm(t: Translate, project: Project): string {
  const published = project.publication !== null;
  if (published) {
    return t("project.delete_confirm_published", project.name, publicAddressOf(project.publication?.slug ?? ""));
  }
  switch (project.service_ids.length) {
    case 0:
      return t("project.delete_confirm_none", project.name);
    case 1:
      return t("project.delete_confirm_one", project.name);
    default:
      return t("project.delete_confirm", project.service_ids.length, project.name);
  }
}
