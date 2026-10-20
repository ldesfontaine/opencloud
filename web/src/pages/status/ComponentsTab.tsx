import { Link } from "react-router";

import type { StatusComponent, StatusComponentsResponse } from "../../api/status";
import { Card, CardBody, EmptyCard } from "../../components/Card";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { Subject, Table } from "../../components/Table";
import type { Resource } from "../../hooks/useResource";
import { useT } from "../../i18n/context";
import "../StatusAdmin.scss";
import { StatusPagePill } from "./StatusPagePill";

export function ComponentsTab({ components }: { components: Resource<StatusComponentsResponse> }) {
  const t = useT();
  return (
    <Loaded resource={components}>
      {(response) => (
        <>
          <Card>
            <CardBody>
              <span className="machine-state">
                <span className="field-label">{t("statuspage.global_label")}</span>
                {response.global === "" ? <Pill tone="neutral">{t("statuspage.global_none")}</Pill> : <StatusPagePill state={response.global} />}
              </span>
            </CardBody>
          </Card>
          {response.components.length === 0 ? (
            <EmptyCard icon="eye" title={t("statuspage.components_empty_title")} text={t("statuspage.components_empty_text")} />
          ) : (
            <Card className="scroll-x">
              <Table>
                <thead>
                  <tr>
                    <th>{t("statuspage.col_component")}</th>
                    <th className="wide-only">{t("statuspage.col_objects")}</th>
                    <th className="wide-only">{t("statuspage.col_derived")}</th>
                    <th className="th-end">{t("statuspage.col_effective")}</th>
                  </tr>
                </thead>
                <tbody>
                  {response.components.map((component) => (
                    <ComponentRow key={component.id} component={component} />
                  ))}
                </tbody>
              </Table>
            </Card>
          )}
        </>
      )}
    </Loaded>
  );
}

function ComponentRow({ component }: { component: StatusComponent }) {
  const t = useT();
  const names = component.members.map((member) => member.name || t("component.missing")).join(", ");
  const imposed = component.effective !== component.derived;
  return (
    <tr>
      <td>
        <Link className="svc" to={`/page-statut/composants/${component.id}`}>
          <Subject icon="eye">{component.name}</Subject>
        </Link>
      </td>
      <td className="wide-only">
        <span className="cell-stack">
          <span>{t(component.members.length === 1 ? "statuspage.objects_count" : "statuspage.objects_counts", component.members.length)}</span>
          {names !== "" && <span className="secondary">{names}</span>}
        </span>
      </td>
      <td className="wide-only">
        <StatusPagePill state={component.derived} />
      </td>
      <td className="td-end">
        <span className="cell-stack end">
          <StatusPagePill state={component.effective} />
          {component.effective === "" && <span className="secondary">{t("statuspage.hidden")}</span>}
          {imposed && component.effective !== "" && <span className="secondary">{t("statuspage.imposed")}</span>}
        </span>
      </td>
    </tr>
  );
}
