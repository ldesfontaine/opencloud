import { useState } from "react";

import { fetchMachineDeletePlan } from "../api/machines";
import { confirmVerbRequest, rejectVerbRequest, useVerbRequests, type VerbRequest } from "../api/verbRequests";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { requestedBy, requestExpiry, requestPhrase } from "../lib/verbRequests";
import { Button } from "./Button";
import { Card, CardBody, CardHeader } from "./Card";
import { DeletePlanDialog } from "./DeletePlanDialog";
import { Failure } from "./Failure";
import "./VerbRequestsCard.scss";

// La carte « gestes à confirmer » de la vue d'ensemble : ce qu'un agent a
// demandé par un jeton et qui ne se défait pas. Rien n'est fait tant que
// l'opérateur n'a pas confirmé ; la demande expire seule. Sans demande en
// attente, la carte n'est pas là.
export function VerbRequestsCard() {
  const t = useT();
  const requests = useVerbRequests();
  const pending = requests.data?.requests ?? [];
  if (pending.length === 0) {
    return null;
  }
  return (
    <Card>
      <CardHeader title={t("verb_request.title")} />
      <CardBody gap={8}>
        <p className="secondary">{t("verb_request.text")}</p>
        <ul className="verb-request-list">
          {pending.map((request) => (
            <VerbRequestRow key={request.id} request={request} />
          ))}
        </ul>
      </CardBody>
    </Card>
  );
}

function VerbRequestRow({ request }: { request: VerbRequest }) {
  const t = useT();
  const now = useNow();
  const rejection = useAction();
  const [confirming, setConfirming] = useState(false);
  return (
    <li className="verb-request-item">
      <span className="verb-request-text">
        <b>{requestPhrase(t, request)}</b>
        <span className="secondary">
          {requestedBy(t, request)} · {requestExpiry(t, request, now)}
        </span>
        <Failure error={rejection.error} />
      </span>
      <span className="verb-request-actions">
        <Button small variant="danger" onClick={() => setConfirming(true)}>
          {t("verb_request.confirm")}
        </Button>
        <Button small disabled={rejection.busy} onClick={() => rejection.run(() => rejectVerbRequest(request.id), { topic: "verb_requests" })}>
          {t("verb_request.reject")}
        </Button>
      </span>
      {confirming && (
        <DeletePlanDialog
          question={t("machine.remove_confirm", request.object.name)}
          confirmLabel={t("verb_request.confirm_label")}
          loadPlan={() => fetchMachineDeletePlan(request.object.id)}
          perform={() => confirmVerbRequest(request.id)}
          topic="all"
          onDone={() => setConfirming(false)}
          onClose={() => setConfirming(false)}
        />
      )}
    </li>
  );
}
