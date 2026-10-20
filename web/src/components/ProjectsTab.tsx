import { Link } from "react-router";

import { useProjects } from "../api/directory";
import { useT } from "../i18n/context";
import { projectsSubtitle } from "../lib/directory";
import { Button } from "./Button";
import { Card, Empty } from "./Card";
import { Failure } from "./Failure";
import { PageHead } from "./PageHead";
import { ServicesTabs } from "./ServicesTabs";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// L'onglet Projets : les groupes sous lesquels l'opérateur range ses
// services déclarés.
export function ProjectsTab() {
  const t = useT();
  const projects = useProjects();
  const all = projects.data?.projects ?? [];
  return (
    <>
      <PageHead
        title={t("nav.services")}
        subtitle={projects.data ? projectsSubtitle(t, all.length) : ""}
        actions={
          <Button variant="primary" icon="plus" to="/services/projets/nouveau">
            {t("project.add")}
          </Button>
        }
      />
      <ServicesTabs />
      {projects.error && <Failure error={projects.error} />}
      {projects.data && all.length === 0 && (
        <Card>
          <Empty icon="archive" title={t("project.empty_title")} text={t("project.empty_text")} />
        </Card>
      )}
      {projects.data && all.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("project.col_project")}</th>
                <th className="wide-only">{t("project.col_title")}</th>
                <th className="th-num th-end">{t("project.col_services")}</th>
              </tr>
            </thead>
            <tbody>
              {all.map((project) => (
                <tr key={project.id}>
                  <td>
                    <Link className="svc" to={`/services/projets/${project.id}`}>
                      <Subject icon="archive">{project.name}</Subject>
                    </Link>
                  </td>
                  <td className="wide-only">{project.title !== "" ? project.title : <span className="muted">—</span>}</td>
                  <td className="num td-num td-end">{project.service_ids.length}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}
