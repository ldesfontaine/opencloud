import { useEffect, useRef, useState } from "react";

import { reasonText, stateTones } from "./ActionsCard";
import { deployService, useDeployPlan, type DeployPlan } from "../api/directory";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { formatWhen } from "../lib/time";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "./Card";
import { Fold } from "./Disclosure";
import { Failure } from "./Failure";
import { Textarea, Field } from "./Field";
import { Pill } from "./Pill";
import "./DeployCard.scss";

// Un ordre de déploiement encore en route : un second ne part pas avant lui.
const inFlight = new Set(["queued", "sent", "accepted"]);

// Déployer un service d'origine Compose : le plan (où, quoi), le dernier
// ordre et son état, et le geste. Le compose.yml est celui que le serveur
// génère de l'annuaire, montré tel qu'il sera écrit ; l'.env se tape à la
// confirmation, part dans l'ordre signé et ne se relit nulle part.
export function DeployCard({ serviceID }: { serviceID: string }) {
  const t = useT();
  const plan = useDeployPlan(serviceID);
  const [asking, setAsking] = useState(false);
  const data = plan.data;
  const running = data?.last_run != null && inFlight.has(data.last_run.state);
  const blocked = data === null || data.right === "missing" || running;
  return (
    <Card>
      <CardHeader
        title={t("deploy.title")}
        aside={
          data !== null && (
            <Button small variant="primary" icon="box" onClick={() => setAsking(true)} disabled={blocked}>
              {t(data.deployed ? "deploy.redeploy" : "deploy.deploy")}
            </Button>
          )
        }
      />
      <CardBody gap={10}>
        <p className="secondary">{t("deploy.subtitle")}</p>
        {plan.error && <Failure error={plan.error} />}
        {data !== null && <PlanBody plan={data} />}
      </CardBody>
      {asking && data !== null && <DeployDialog plan={data} onClose={() => setAsking(false)} />}
    </Card>
  );
}

function PlanBody({ plan }: { plan: DeployPlan }) {
  const t = useT();
  const now = useNow();
  const run = plan.last_run;
  return (
    <>
      {plan.right === "missing" && <Note tone="warn">{t("deploy.right_missing")}</Note>}
      <KeyValue label={t("deploy.plan_path")} value={plan.path} mono />
      <KeyValue
        label={t("deploy.plan_units")}
        value={
          <span className="stack">
            {plan.data_dirs.map((name) => (
              <span key={name} className="mono">
                {name} · {t("deploy.plan_unit_data", `data/${name}`)}
              </span>
            ))}
            {plan.data_dirs.length === 0 && <span className="muted">—</span>}
          </span>
        }
      />
      <Fold label={t("deploy.plan_compose")} initiallyOpen={false}>
        <pre className="deploy-compose mono">{plan.compose}</pre>
      </Fold>
      <KeyValue
        label={t("deploy.last_title")}
        value={
          run === null ? (
            <span className="muted">{t("deploy.last_none")}</span>
          ) : (
            <span className="stack">
              <span className="cluster">
                <Pill tone={stateTones[run.state]}>{t(`action.state_${run.state}`)}</Pill>
                <span className="secondary">
                  {t(`action.kind_${run.kind.split(".").join("_")}`)} · {formatWhen(run.requested_at, now)}
                </span>
              </span>
              {run.reason !== "" && <span className="secondary">{reasonText(t, run.reason)}</span>}
              {run.output.length > 0 && <pre className="deploy-output secondary">{run.output.map((line) => line.line).join("\n")}</pre>}
            </span>
          )
        }
      />
    </>
  );
}

// La confirmation : l'.env facultatif, puis l'ordre. La boîte est montée
// pour un geste et démontée à sa fermeture : l'.env tapé ne survit pas à
// l'ordre, il n'est jamais écrit dans le navigateur.
function DeployDialog({ plan, onClose }: { plan: DeployPlan; onClose: () => void }) {
  const t = useT();
  const dialog = useRef<HTMLDialogElement>(null);
  const action = useAction();
  const [env, setEnv] = useState("");
  useEffect(() => {
    if (dialog.current !== null && !dialog.current.open) {
      dialog.current.showModal();
    }
  }, []);
  const send = () => {
    action.run(() => deployService(plan.service_id, plan.deployed, env === "" ? null : env), {
      topic: "actions",
      onDone: () => dialog.current?.close(),
    });
  };
  return (
    <dialog ref={dialog} className="deploy-dialog" onClose={onClose}>
      <p className="deploy-dialog-question">{t(plan.deployed ? "deploy.confirm_redeploy" : "deploy.confirm")}</p>
      <p className="mono deploy-dialog-path">
        {plan.machine_name} · {plan.path}
      </p>
      <Failure error={action.error} />
      <Field label={t("deploy.env_label")} htmlFor="deploy-env" help={t("deploy.env_help")}>
        <Textarea
          id="deploy-env"
          mono
          rows={6}
          value={env}
          onChange={(event) => setEnv(event.target.value)}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
        />
      </Field>
      <div className="deploy-dialog-actions">
        <Button onClick={() => dialog.current?.close()}>{t("deploy.cancel")}</Button>
        <Button variant="primary" icon="box" onClick={send} disabled={action.busy}>
          {t(plan.deployed ? "deploy.redeploy" : "deploy.deploy")}
        </Button>
      </div>
    </dialog>
  );
}
