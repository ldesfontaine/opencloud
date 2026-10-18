import { useState } from "react";
import { Link } from "react-router";

import { api, errorKey } from "../api/client";
import type { ContainerDirectory, Container } from "../api/types";
import { useT } from "../i18n/context";
import { adoptPath, isOrphan, orphanText } from "../lib/gaps";
import { useRefresh } from "../lib/refresh";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, Note } from "./Card";
import { Pill } from "./Pill";

// Ce que l'annuaire sait de ce conteneur : l'unité déclarée à laquelle il se
// rattache, ou, faute d'elle, de quoi l'adopter ou le dire voulu. Une fiche
// archivée garde son dernier rattachement, mais n'a rien à adopter : le
// conteneur n'existe plus. Un conteneur dont le label ne vise rien est dit
// « label orphelin », avec le motif et les deux valeurs : le corriger, ou le
// retirer, ne passe pas par l'adoption.
export function DirectoryCard({ container, directory }: { container: Container; directory: ContainerDirectory | null }) {
  const t = useT();
  const { refresh } = useRefresh();
  const [error, setError] = useState<string | null>(null);
  if (directory === null) {
    return null;
  }
  const isArchived = container.archived_at !== null;
  if (!directory.declared && isArchived) {
    return null;
  }
  const send = (request: Promise<unknown>) => {
    setError(null);
    request.then(() => refresh("directory")).catch((failed: unknown) => setError(errorKey(failed)));
  };
  const orphan = isOrphan(directory);
  const key = { machine_id: container.machine_id, kind: directory.key_kind, value: directory.key_value };
  const unignore = new URLSearchParams({ machine: key.machine_id, kind: key.kind, value: key.value });
  return (
    <Card>
      <CardHeader title={t("gap.card_title")} />
      <CardBody gap={10}>
        {error !== null && <Note tone="danger">{t(error)}</Note>}
        {directory.declared ? (
          <>
            <span className="cluster">
              <Link to={`/services/annuaire/${directory.service_id}`}>{t(isArchived ? "gap.was_linked" : "gap.linked", directory.service_name)}</Link>
              {directory.weak && (
                <span title={t("gap.weak_help")}>
                  <Pill tone="neutral" dot={false}>
                    {t("gap.weak")}
                  </Pill>
                </span>
              )}
            </span>
            <span className="secondary">
              <span className="mono">{directory.unit_name}</span>
              {directory.match_level !== "" && ` · ${t(`gap.level_${directory.match_level}`)}`}
            </span>
          </>
        ) : (
          <>
            <span className="cluster">
              <Pill tone={directory.ignored ? "neutral" : "warn"}>{t(directory.ignored ? "gap.ignored" : "gap.undeclared")}</Pill>
              {orphan && <Pill tone="warn">{t("gap.orphan")}</Pill>}
              <span className="secondary">
                {t(`gap.key_${directory.key_kind}`)} <span className="mono">{directory.key_value}</span>
              </span>
            </span>
            {orphan && (
              <>
                <span className="secondary">{orphanText(t, directory)}</span>
                <span className="secondary">
                  {t("gap.labels_title")} <code>{directory.label_service === "" ? "—" : directory.label_service}</code> ·{" "}
                  <code>{directory.label_unit === "" ? "—" : directory.label_unit}</code>
                </span>
                <span className="secondary">{t("gap.orphan_help")}</span>
              </>
            )}
            <span className="cluster">
              {directory.ignored ? (
                <Button small variant="ghost" onClick={() => send(api.delete(`/api/directory/ignored?${unignore.toString()}`))}>
                  {t("gap.unignore")}
                </Button>
              ) : (
                <>
                  {!orphan && (
                    <Button small variant="primary" to={adoptPath(container.id)}>
                      {t("gap.adopt")}
                    </Button>
                  )}
                  <Button small variant="ghost" onClick={() => send(api.post("/api/directory/ignored", key))}>
                    {t("gap.ignore")}
                  </Button>
                </>
              )}
            </span>
          </>
        )}
      </CardBody>
    </Card>
  );
}
