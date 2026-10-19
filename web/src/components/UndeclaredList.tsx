import { useState } from "react";
import { Link } from "react-router";

import { api } from "../api/client";
import type { DeclaredService, UndeclaredGroup } from "../api/types";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { adoptIntoPath, adoptionTarget, adoptPath, containerCount, hasProposals, ignoredCount, ignoreBody, orphanKey, proposedExportPath, shownUndeclared, unignorePath } from "../lib/gaps";
import { Button } from "./Button";
import { Card, CardHeader, Empty } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// Ce que les machines montrent sans que l'annuaire le déclare, par clé :
// le projet Compose entier, ou le conteneur seul. L'opérateur l'adopte
// (le formulaire se pré-remplit) ou le dit voulu en l'ignorant ; les ignorés
// se cachent derrière un bouton, mais ne disparaissent pas. Un conteneur dont
// le label ne vise rien porte une pastille avec le motif : on le corrige par le
// label, on ne l'adopte pas. Le fichier de l'annuaire peut aussi se proposer
// d'un coup, à relire avant de l'appliquer.
export function UndeclaredList({ groups, declared }: { groups: UndeclaredGroup[]; declared: DeclaredService[] }) {
  const t = useT();
  const [showIgnored, setShowIgnored] = useState(false);
  const shown = shownUndeclared(groups, showIgnored);
  const hidden = ignoredCount(groups);
  const names = new Map(declared.map((service) => [service.id, service.name]));

  const action = useAction();
  const ignore = (group: UndeclaredGroup) => {
    action.run(() => api.post("/api/directory/ignored", ignoreBody(group)), { topic: "directory" });
  };
  const stopIgnoring = (group: UndeclaredGroup) => {
    action.run(() => api.delete(unignorePath(group)), { topic: "directory" });
  };
  const toggle =
    hidden === 0 ? undefined : (
      <Button small variant="ghost" onClick={() => setShowIgnored((current) => !current)}>
        {showIgnored ? t("gap.hide_ignored") : t("gap.show_ignored", hidden)}
      </Button>
    );

  return (
    <Card className="scroll-x">
      <CardHeader
        title={t("gap.undeclared_title")}
        aside={
          <span className="cluster">
            {hasProposals(groups) && (
              <Button small variant="ghost" icon="file" href={proposedExportPath} download="">
                {t("manifest.export_propose")}
              </Button>
            )}
            <span className="num">{containerCount(shown)}</span>
          </span>
        }
      />
      <Failure error={action.error} />
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
                        <span key={container.id} className="cluster">
                          <Link className="mono" to={`/services/conteneurs/${container.id}`}>
                            {container.name}
                          </Link>
                          {container.label_problem !== "" && (
                            <span title={t(orphanKey(container.label_problem))}>
                              <Pill tone="warn" dot={false}>
                                {t("gap.orphan")}
                              </Pill>
                            </span>
                          )}
                        </span>
                      ))}
                    </span>
                  </td>
                  <td className="td-end">
                    <span className="cluster">
                      {group.ignored ? (
                        <Button small variant="ghost" onClick={() => stopIgnoring(group)}>
                          {t("gap.unignore")}
                        </Button>
                      ) : (
                        <>
                          {group.adoptable && (
                            <Button small variant="primary" to={group.service_id === "" ? adoptPath(adoptionTarget(group)) : adoptIntoPath(group.service_id, adoptionTarget(group))}>
                              {group.service_id === "" ? t("gap.adopt") : t("gap.adopt_into", names.get(group.service_id) ?? group.key_value)}
                            </Button>
                          )}
                          <Button small variant="ghost" onClick={() => ignore(group)}>
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
