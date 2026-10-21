import { useState } from "react";
import { useSearchParams } from "react-router";

import { allowMCPConsent, denyMCPConsent, useMCPConsent, type MCPConsent } from "../api/mcp";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Checkbox } from "../components/Field";
import { Brand } from "../components/Layout";
import { Loaded } from "../components/Loaded";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { lifetimesOf, writeOfferOf } from "../lib/mcp";
import { formatClock } from "../lib/time";
import "./OAuthConsent.scss";

// Le seul paramètre que la page lit de son adresse : l'identifiant opaque de
// la demande, posé par /oauth/authorize. Le reste vient du serveur.
const consentParam = "demande";

// La page où l'opérateur consent pour un agent IA. Hors de l'interface,
// comme la première configuration : la marque, une carte, deux boutons.
export function OAuthConsent() {
  const [params] = useSearchParams();
  const consent = useMCPConsent(params.get(consentParam) ?? "");
  return (
    <main className="oauth-consent">
      <div className="oauth-consent-column">
        <Brand />
        <Loaded resource={consent} notFound={<Failure error="mcp.consent_unknown" />}>
          {(data) => <ConsentCard consent={data} />}
        </Loaded>
      </div>
    </main>
  );
}

// La réponse rend l'adresse de retour du client, avec le code ou
// access_denied : le navigateur y part, et la page reste occupée jusque-là.
function ConsentCard({ consent }: { consent: MCPConsent }) {
  const t = useT();
  const action = useAction();
  const [write, setWrite] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const offer = writeOfferOf(consent);
  const leave = (answer: { redirect: string }) => {
    setLeaving(true);
    window.location.assign(answer.redirect);
  };
  const allow = () => action.run(() => allowMCPConsent(consent.id, offer === "checkbox" && write), { onDone: leave });
  const deny = () => action.run(() => denyMCPConsent(consent.id), { onDone: leave });
  const busy = action.busy || leaving;

  return (
    <Card className="form-card">
      <CardHeader title={t("mcp.consent_title")} />
      <CardBody>
        <p>{t("mcp.consent_intro")}</p>
        <KeyValue label={t("mcp.consent_client_label")} value={consent.client_id} mono />
        <KeyValue label={t("mcp.consent_redirect_label")} value={consent.redirect_uri} mono />
        <p className="secondary">{t("mcp.consent_redirect_help")}</p>
      </CardBody>
      <CardBody>
        <div className="field">
          <span className="field-label">{t("mcp.consent_grants_label")}</span>
          <p>{t("mcp.tools", consent.read_tools)}</p>
          <p className="secondary">{t("mcp.never")}</p>
        </div>
        {offer === "unavailable" && <Note tone="warn">{t("mcp.consent_write_unavailable")}</Note>}
        {offer === "checkbox" && (
          <div className="field">
            <Checkbox label={t("mcp.consent_write_label")} checked={write} onChange={setWrite} />
            <span className="field-help">{t("mcp.consent_write_help")}</span>
          </div>
        )}
      </CardBody>
      <CardBody>
        <div className="field">
          <span className="field-label">{t("mcp.consent_duration_label")}</span>
          <p>{lifetimesOf(t, consent.access_seconds, consent.refresh_seconds)}</p>
          <p className="secondary">{t("mcp.consent_revoke")}</p>
        </div>
        <p className="secondary">{t("mcp.consent_expires", formatClock(consent.expires_at))}</p>
        <Failure error={action.error} />
        <div className="cluster">
          <Button variant="primary" icon="check" onClick={allow} disabled={busy}>
            {t("mcp.consent_allow")}
          </Button>
          <Button icon="x" onClick={deny} disabled={busy}>
            {t("mcp.consent_deny")}
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
