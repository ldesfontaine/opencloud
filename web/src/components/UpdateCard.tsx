import { useState } from "react";

import { api } from "../api/client";
import type { Service, UpdatePolicy } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { ago } from "../lib/time";
import { checkText, needsComposeEdit, shortDigest, updateAvailable } from "../lib/updates";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "./Card";
import { TokenBox } from "./Copy";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import "./UpdateCard.scss";

// La carte Mise à jour d'un service : ce que l'agent a constaté sur
// l'image, la commande à copier, jamais exécutée, et ce que l'opérateur
// en veut : vérifier maintenant, épingler, exclure.
export function UpdateCard({ service, reload }: { service: Service; reload: () => void }) {
  const { t } = useI18n();
  const now = useNow();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);
  const check = service.image_check;
  const available = updateAvailable(service);

  const setPolicy = (policy: UpdatePolicy) => {
    setError(null);
    api
      .put(`/api/services/${service.id}/update-policy`, { policy })
      .then(() => {
        reload();
        refresh();
      })
      .catch(setError);
  };
  const checkNow = () => {
    setError(null);
    api.post(`/api/machines/${service.machine_id}/actions/check-updates`).catch(setError);
  };

  return (
    <Card>
      <CardHeader title={t("update.title")} aside={check !== null ? t("update.checked_ago", ago(t, check.checked_at, now)) : undefined} />
      <CardBody gap={12}>
        {error !== null && <Failure error={error} />}
        <div className="update-state">
          {service.update_policy !== "" && <Pill tone="neutral">{t(`update.policy_${service.update_policy}`)}</Pill>}
          {available && check !== null && <Pill tone="accent">{t(`update.kind_${check.kind}`)}</Pill>}
          <span>{check === null ? t("update.never_checked") : checkText(t, check)}</span>
        </div>
        {service.update_policy === "pinned" && <span className="secondary">{t("update.pinned_note")}</span>}
        {service.update_policy === "excluded" && <span className="secondary">{t("update.excluded_note")}</span>}
        {check !== null && check.local_digest !== "" && (
          <div className="update-digests">
            <KeyValue label={t("update.local_digest")} value={shortDigest(check.local_digest)} mono />
            {check.remote_digest !== "" && <KeyValue label={t("update.remote_digest")} value={shortDigest(check.remote_digest)} mono />}
            {check.newer_digest !== "" && <KeyValue label={t("update.newer_digest")} value={shortDigest(check.newer_digest)} mono />}
          </div>
        )}
        {available && check !== null && check.command !== "" && (
          <div className="update-command">
            <span className="secondary">{t("update.command_title")}</span>
            {needsComposeEdit(service, check) && <Note tone="warn">{t("update.edit_compose", check.newer_tag, service.compose_file !== "" ? service.compose_file : "compose.yaml")}</Note>}
            <TokenBox value={check.command} />
            {service.compose_service === "" && <span className="secondary">{t("update.recreate_hint")}</span>}
          </div>
        )}
        <div className="update-actions">
          <Button small icon="refresh" onClick={checkNow}>
            {t("update.check_now")}
          </Button>
          {service.update_policy === "pinned" ? (
            <Button small onClick={() => setPolicy("")}>
              {t("update.unpin")}
            </Button>
          ) : (
            <Button small onClick={() => setPolicy("pinned")}>
              {t("update.pin")}
            </Button>
          )}
          {service.update_policy === "excluded" ? (
            <Button small onClick={() => setPolicy("")}>
              {t("update.include")}
            </Button>
          ) : (
            <Button small onClick={() => setPolicy("excluded")}>
              {t("update.exclude")}
            </Button>
          )}
        </div>
      </CardBody>
    </Card>
  );
}
