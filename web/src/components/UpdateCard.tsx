import { useState } from "react";

import { Link } from "react-router";

import { api } from "../api/client";
import type { ContainerDirectory, Service, UpdatePolicy } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { adoptPath } from "../lib/gaps";
import { useRefresh } from "../lib/refresh";
import { ago } from "../lib/time";
import { checkText, imageLineChange, shortDigest, updateAvailable } from "../lib/updates";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue } from "./Card";
import { CopyButton, TokenBox } from "./Copy";
import { Fold } from "./Disclosure";
import { Failure } from "./Failure";
import { Select } from "./Field";
import { Pill } from "./Pill";
import { SupportBlock } from "./SupportBlock";
import "./UpdateCard.scss";

// Le nom que Compose prend quand le label ne dit pas le fichier.
const defaultComposeFile = "compose.yaml";

// La carte Mise à jour d'un service : ce que l'agent a constaté sur
// l'image, la ligne du fichier Compose à changer, la commande à copier,
// jamais exécutée, et vérifier maintenant. Quand la fin de support est
// activée, le support de la version suit, sous un trait.
// Les empreintes et la politique sont repliées : on les règle une fois. La
// politique vit sur l'unité de l'annuaire, pas sur ce conteneur : elle
// survit à sa recréation, et un conteneur non déclaré n'en a pas à régler.
// Le pli s'ouvre seul quand une politique est posée.
const policies: readonly UpdatePolicy[] = ["watch", "hold", "ignore"];

export function UpdateCard({ service, directory, reload }: { service: Service; directory: ContainerDirectory | null; reload: () => void }) {
  const { t } = useI18n();
  const now = useNow();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);
  const [checking, setChecking] = useState<"idle" | "asking" | "asked">("idle");
  const check = service.image_check;
  const available = updateAvailable(service);
  const lineChange = check !== null ? imageLineChange(service, check) : null;

  const unitID = directory !== null && directory.declared ? directory.unit_id : "";
  const setPolicy = (policy: UpdatePolicy) => {
    setError(null);
    api
      .put(`/api/directory/units/${unitID}/update-policy`, { policy })
      .then(() => {
        reload();
        refresh();
      })
      .catch(setError);
  };
  // La machine vérifie à son rythme : la carte dit que la demande est
  // partie, le constat se relit seul quand l'agent a répondu.
  const checkNow = () => {
    setError(null);
    setChecking("asking");
    api
      .post(`/api/machines/${service.machine_id}/actions/check-updates`)
      .then(() => setChecking("asked"))
      .catch((failure: unknown) => {
        setError(failure);
        setChecking("idle");
      });
  };

  return (
    <Card>
      <CardHeader title={t("update.title")} aside={check !== null ? t("update.checked_ago", ago(t, check.checked_at, now)) : undefined} />
      <CardBody gap={12}>
        {error !== null && <Failure error={error} />}
        <div className="update-state">
          {service.update_policy !== "watch" && <Pill tone="neutral">{t(`update.policy_${service.update_policy}`)}</Pill>}
          {available && check !== null && <Pill tone="accent">{t(`update.kind_${check.kind}`)}</Pill>}
          <span>{check === null ? t("update.never_checked") : checkText(t, check)}</span>
        </div>
        {service.update_policy === "hold" && <span className="secondary">{t("update.hold_note")}</span>}
        {service.update_policy === "ignore" && <span className="secondary">{t("update.ignore_note")}</span>}
        {available && check !== null && check.command !== "" && (
          <div className="update-command">
            <span className="secondary">{t("update.command_title")}</span>
            {lineChange !== null && (
              <>
                <span>{t("update.edit_compose", service.compose_file !== "" ? service.compose_file : defaultComposeFile, service.compose_service)}</span>
                <div className="update-diff">
                  <pre className="snippet mono">
                    <span className="diff-line diff-removed">- {lineChange.before}</span>
                    <span className="diff-line diff-added">+ {lineChange.after}</span>
                  </pre>
                  <CopyButton text={lineChange.after} />
                </div>
                <span>{t("update.then_run")}</span>
              </>
            )}
            <TokenBox value={check.command} />
            {service.compose_service === "" && <span className="secondary">{t("update.recreate_hint")}</span>}
          </div>
        )}
        <div className="update-actions">
          <Button small icon="refresh" onClick={checkNow} disabled={checking === "asking"}>
            {t("update.check_now")}
          </Button>
          {checking === "asked" && <span className="secondary">{t("update.check_asked")}</span>}
        </div>
        <Fold label={t("update.details")} initiallyOpen={service.update_policy !== "watch"}>
          {check !== null && check.local_digest !== "" && (
            <div className="update-digests">
              <KeyValue label={t("update.local_digest")} value={shortDigest(check.local_digest)} mono />
              {check.remote_digest !== "" && <KeyValue label={t("update.remote_digest")} value={shortDigest(check.remote_digest)} mono />}
              {check.newer_digest !== "" && <KeyValue label={t("update.newer_digest")} value={shortDigest(check.newer_digest)} mono />}
            </div>
          )}
          <div className="update-policy">
            <Select
              aria-label={t("directory.update_policy_label")}
              value={service.update_policy}
              disabled={unitID === ""}
              onChange={(event) => setPolicy(event.target.value as UpdatePolicy)}
            >
              {policies.map((policy) => (
                <option key={policy} value={policy}>
                  {t(`update.policy_${policy}`)}
                </option>
              ))}
            </Select>
            {directory !== null && directory.declared ? (
              <Link to={`/services/annuaire/${directory.service_id}`}>{t("update.policy_on_unit", directory.unit_name, directory.service_name)}</Link>
            ) : (
              <span className="secondary">
                {t("update.policy_needs_declaration")}
                {service.archived_at === null && (
                  <>
                    {" "}
                    <Link to={adoptPath(service.id)}>{t("gap.adopt")}</Link>
                  </>
                )}
              </span>
            )}
          </div>
        </Fold>
        {service.lifecycle !== undefined && <SupportBlock lifecycle={service.lifecycle} subject="image" />}
      </CardBody>
    </Card>
  );
}
