import { useState } from "react";
import { Link } from "react-router";

import { api, errorKey } from "../api/client";
import type { DeclaredService, UndeclaredGroup } from "../api/types";
import { useT } from "../i18n/context";
import { adoptIntoPath, adoptionTarget, adoptPath, containerCount, ignoredCount, ignoreBody, shownUndeclared, unignorePath } from "../lib/gaps";
import { useRefresh } from "../lib/refresh";
import { Button } from "./Button";
import { Card, CardHeader, Empty, Note } from "./Card";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// Ce que les machines montrent sans que l'annuaire le déclare, par clé :
// le projet Compose entier, ou le conteneur seul. L'opérateur l'adopte
// (le formulaire se pré-remplit) ou le dit voulu en l'ignorant ; les ignorés
// se cachent derrière un bouton, mais ne disparaissent pas.
export function UndeclaredList({ groups, declared }: { groups: UndeclaredGroup[]; declared: DeclaredService[] }) {
  const t = useT();
  const { refresh } = useRefresh();
  const [showIgnored, setShowIgnored] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const shown = shownUndeclared(groups, showIgnored);
  const hidden = ignoredCount(groups);
  const names = new Map(declared.map((service) => [service.id, service.name]));

  const send = (request: Promise<unknown>) => {
    setError(null);
    request.then(() => refresh("directory")).catch((failed: unknown) => setError(errorKey(failed)));
  };
  const toggle =
    hidden === 0 ? undefined : (
      <Button small variant="ghost" onClick={() => setShowIgnored((current) => !current)}>
        {showIgnored ? t("gap.hide_ignored") : t("gap.show_ignored", hidden)}
      </Button>
    );

  return (
    <Card className="scroll-x">
      <CardHeader title={t("gap.undeclared_title")} aside={<span className="num">{containerCount(shown)}</span>} />
      {error !== null && <Note tone="danger">{t(error)}</Note>}
      {shown.length === 0 ? (
        <Empty text={t("gap.undeclared_empty")} action={toggle} />
      ) : (
        <>
          <Table>
            <tbody>
              {shown.map((group) => (
                <tr key={`${group.machine_id} ${group.key_kind} ${group.key_value}`}>
                  <td>
                    <Subject icon="box">
                      <span className="svc-name">
                        <span className="mono">{group.key_value}</span>
                        <span className="mono muted">{t(`gap.key_${group.key_kind}`)}</span>
                      </span>
                    </Subject>
                    {group.ignored && (
                      <Pill tone="neutral" dot={false}>
                        {t("gap.ignored")}
                      </Pill>
                    )}
                  </td>
                  <td className="wide-only">
                    <Link to={`/machines/${group.machine_id}`}>{group.machine_name}</Link>
                  </td>
                  <td>
                    <span className="stack">
                      {group.containers.map((container) => (
                        <Link key={container.id} className="mono" to={`/services/${container.id}`}>
                          {container.name}
                        </Link>
                      ))}
                    </span>
                  </td>
                  <td className="td-end">
                    <span className="cluster">
                      {group.ignored ? (
                        <Button small variant="ghost" onClick={() => send(api.delete(unignorePath(group)))}>
                          {t("gap.unignore")}
                        </Button>
                      ) : (
                        <>
                          <Button small variant="primary" to={group.service_id === "" ? adoptPath(adoptionTarget(group)) : adoptIntoPath(group.service_id, adoptionTarget(group))}>
                            {group.service_id === "" ? t("gap.adopt") : t("gap.adopt_into", names.get(group.service_id) ?? group.key_value)}
                          </Button>
                          <Button small variant="ghost" onClick={() => send(api.post("/api/directory/ignored", ignoreBody(group)))}>
                            {t("gap.ignore")}
                          </Button>
                        </>
                      )}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </Table>
          {toggle !== undefined && <div className="card-b">{toggle}</div>}
        </>
      )}
    </Card>
  );
}
