import { useState } from "react";

import { disableMCP, enableMCP, regenerateMCPSecret, type MCPState } from "../../api/mcp";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "../../components/Card";
import { TokenBox } from "../../components/Copy";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { claudeCodeCommand, lifetimesOf } from "../../lib/mcp";
import { ago } from "../../lib/time";
import { MCPRedirectURIsSection } from "./MCPRedirectURIsSection";
import { MCPSessionsSection } from "./MCPSessionsSection";
import { MCPTokensSection } from "./MCPTokensSection";

// L'accès d'un agent IA : activer, couper, régénérer le secret, puis, une
// fois activé, ce qu'il faut au client (adresse, identifiant, secret), les
// adresses de retour, les jetons et les sessions. Le secret ne se montre
// qu'une fois, dans l'état de la section ; relue, elle ne montre que le masque.
export function MCPSection({ state, reload }: { state: MCPState; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  const [secret, setSecret] = useState<string | null>(null);

  // On relit même après un refus : l'accès a pu changer ailleurs.
  const enable = () => {
    action.run(() => enableMCP(), { onDone: (response) => setSecret(response.secret), onSettled: reload });
  };
  const regenerate = () => {
    action.run(() => regenerateMCPSecret(), {
      confirm: t("mcp.regenerate_confirm"),
      onDone: (response) => setSecret(response.secret),
      onSettled: reload,
    });
  };
  const disable = () => {
    const perform = () => {
      setSecret(null);
      return disableMCP();
    };
    action.run(perform, { confirm: t("mcp.disable_confirm"), onSettled: reload });
  };

  return (
    <>
      <Card className="form-card">
        <CardHeader
          title={t("mcp.title")}
          aside={<Pill tone={state.enabled ? "ok" : "neutral"}>{t(state.enabled ? "mcp.state_enabled" : "mcp.state_disabled")}</Pill>}
        />
        <CardBody>
          <p>{t("mcp.intro")}</p>
          <p className="secondary">{t("mcp.tools", state.tools.reads, state.tools.writes)}</p>
          <p className="secondary">{t("mcp.never")}</p>
          <Failure error={action.error} />
          {!state.enabled && (
            <>
              <p className="secondary">{t("mcp.enable_text")}</p>
              <div className="cluster">
                <Button variant="primary" icon="check" onClick={enable} disabled={action.busy}>
                  {t("mcp.enable")}
                </Button>
              </div>
            </>
          )}
          {state.enabled && (
            <div className="cluster">
              <span className="secondary">{state.enabled_at !== null && t("mcp.enabled_since", ago(t, state.enabled_at, now))}</span>
              <Button variant="danger" icon="x" onClick={disable} disabled={action.busy}>
                {t("mcp.disable")}
              </Button>
            </div>
          )}
        </CardBody>
      </Card>
      {state.enabled && (
        <>
          <Card className="form-card">
            <CardHeader title={t("mcp.client_title")} />
            <CardBody>
              <p className="secondary">{t("mcp.client_text")}</p>
              <div className="field">
                <span className="field-label">{t("mcp.endpoint_label")}</span>
                <TokenBox value={state.endpoint} />
              </div>
              {state.url_local && <Note tone="danger">{t("mcp.url_local")}</Note>}
              <KeyValue label={t("mcp.client_id_label")} value={state.client_id} mono />
              {secret !== null ? (
                <>
                  <Note tone="warn">{t("mcp.secret_once")}</Note>
                  <div className="field">
                    <span className="field-label">{t("mcp.secret_label")}</span>
                    <TokenBox value={secret} />
                  </div>
                </>
              ) : (
                <KeyValue label={t("mcp.secret_label")} value={state.secret_masked} mono />
              )}
              <p className="secondary">{t("mcp.secret_help")}</p>
              <p className="secondary">{lifetimesOf(t, state.lifetimes.access_seconds, state.lifetimes.refresh_seconds)}</p>
              <div className="cluster">
                <Button icon="refresh" onClick={regenerate} disabled={action.busy}>
                  {t("mcp.regenerate")}
                </Button>
              </div>
            </CardBody>
          </Card>
          <MCPRedirectURIsSection state={state} reload={reload} />
          <MCPTokensSection
            tokens={state.tokens}
            limit={state.limits.tokens}
            lifetimes={state.lifetimes.api_token_days}
            defaultDays={state.lifetimes.api_token_default_days}
            reload={reload}
            now={now}
          />
          <MCPSessionsSection sessions={state.sessions} reload={reload} now={now} />
          <Card className="form-card">
            <CardHeader title={t("mcp.stdio_title")} />
            <CardBody>
              <p className="secondary">{t("mcp.stdio_text")}</p>
              <div className="field">
                <span className="field-label">{t("mcp.stdio_label")}</span>
                <TokenBox value={state.stdio_command} />
              </div>
              <div className="field">
                <span className="field-label">{t("mcp.claude_code_label")}</span>
                <TokenBox value={claudeCodeCommand(state.server_name, state.stdio_command)} />
              </div>
            </CardBody>
          </Card>
        </>
      )}
    </>
  );
}
