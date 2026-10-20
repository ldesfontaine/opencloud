import { useState } from "react";

import { changePassword, disableAuth, enableAuth, type AuthEnableRequest, type AuthEnableResponse, type AuthState } from "../../api/auth";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { EnumSelect, Field } from "../../components/Field";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { isForwardInsecure } from "../../lib/auth";
import { ago } from "../../lib/time";
import { AccessCredentialsForm } from "./AccessCredentialsForm";
import { AccessForwardForm } from "./AccessForwardForm";

// Les deux façons d'activer : identifiant et mot de passe, ou l'en-tête que
// pose le mandataire d'authentification.
const enableModes = ["basic", "forward"] as const;

function reloadPage() {
  window.location.reload();
}

// L'accès à l'interface. Activer ou changer le mot de passe rend caducs
// les identifiants du navigateur : la page se recharge, et le navigateur
// les demande dans sa propre fenêtre. Activer révoque aussi tous les
// jetons MCP : la carte dit combien avant de recharger. En mode
// mandataire, il n'y a rien à saisir après coup : la carte montre
// l'en-tête et l'identité que le mandataire transmet.
export function AccessSection({ state }: { state: AuthState }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  const [mode, setMode] = useState<(typeof enableModes)[number]>("basic");
  const [enabledWith, setEnabledWith] = useState<AuthEnableResponse | null>(null);
  const enabled = state.mode !== "none";

  const disable = () => {
    action.run(() => disableAuth(), { confirm: t("auth.disable_confirm"), onDone: reloadPage });
  };
  const changeTo = (password: string) => {
    action.run(() => changePassword(password), { onDone: reloadPage });
  };
  const enable = (request: AuthEnableRequest) => {
    action.run(() => enableAuth(request), { confirm: t("auth.enable_confirm"), onDone: setEnabledWith });
  };

  if (enabledWith !== null) {
    return (
      <Card className="form-card">
        <CardHeader title={t("auth.title")} aside={<Pill tone="ok">{t(`auth.state_${enabledWith.mode}`)}</Pill>} />
        <CardBody>
          <Note tone="warn">{t("auth.mcp_revoked", enabledWith.mcp_revoked.api_tokens, enabledWith.mcp_revoked.sessions)}</Note>
          {enabledWith.mode === "basic" && <p className="secondary">{t("auth.browser_prompt")}</p>}
          <div className="cluster">
            <Button variant="primary" icon="check" onClick={reloadPage}>
              {t("auth.continue")}
            </Button>
          </div>
        </CardBody>
      </Card>
    );
  }

  return (
    <Card className="form-card">
      <CardHeader title={t("auth.title")} aside={<Pill tone={enabled && !isForwardInsecure(state) ? "ok" : "warn"}>{t(`auth.state_${state.mode}`)}</Pill>} />
      <CardBody>
        <p>{t("auth.intro")}</p>
        {!enabled && <Note tone="warn">{t("auth.open_text")}</Note>}
        {isForwardInsecure(state) && <Note tone="warn">{t("auth.forward_insecure")}</Note>}
        {state.mode === "basic" && <KeyValue label={t("auth.username_label")} value={state.username} mono />}
        {state.mode === "forward" && (
          <>
            <KeyValue label={t("auth.header_label")} value={state.header ?? ""} mono />
            <KeyValue label={t("auth.identity_label")} value={state.username} mono />
          </>
        )}
        {enabled && (
          <div className="cluster">
            <span className="secondary">{state.enabled_at !== null && t("auth.enabled_since", ago(t, state.enabled_at, now))}</span>
            <Button variant="danger" icon="x" onClick={disable} disabled={action.busy}>
              {t("auth.disable")}
            </Button>
          </div>
        )}
        {!enabled && (
          <Field label={t("auth.mode_label")} htmlFor="auth-mode">
            <EnumSelect id="auth-mode" value={mode} values={enableModes} labelKey="auth.mode_" onChange={setMode} />
          </Field>
        )}
        <Failure error={action.error} />
      </CardBody>
      {state.mode === "basic" && (
        <AccessCredentialsForm withUsername={false} minLength={state.password_min_length} busy={action.busy} onSubmit={(_username, password) => changeTo(password)} />
      )}
      {!enabled && mode === "basic" && (
        <AccessCredentialsForm
          withUsername
          minLength={state.password_min_length}
          busy={action.busy}
          onSubmit={(username, password) => enable({ username, password })}
        />
      )}
      {!enabled && mode === "forward" && <AccessForwardForm busy={action.busy} onSubmit={(header) => enable({ mode: "forward", header })} />}
    </Card>
  );
}
