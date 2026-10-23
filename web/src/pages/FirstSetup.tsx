import { useState, type ReactNode } from "react";

import { enableAuth, signIn, useAuth, type AuthState } from "../api/auth";
import { offerLocalEnrollment, type TokenResponse } from "../api/machines";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Note } from "../components/Card";
import { TokenBox } from "../components/Copy";
import { Failure } from "../components/Failure";
import { EnumSelect, Field } from "../components/Field";
import { Brand } from "../components/Layout";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { needsFirstSetup } from "../lib/auth";
import { formatRemaining } from "../lib/time";
import { AccessCredentialsForm } from "./settings/AccessCredentialsForm";
import { AccessForwardForm } from "./settings/AccessForwardForm";
import "./FirstSetup.scss";

// Les deux façons de garder l'interface : un compte local, ou le mandataire
// d'authentification qui transmet l'identité.
const setupModes = ["basic", "forward"] as const;

// Tant qu'aucun compte n'existe, le serveur ne sert rien d'autre que la
// création du compte : l'écran de première configuration tient la place de
// l'interface. Rien ne s'affiche avant la réponse du serveur.
export function FirstSetupGate({ children }: { children: ReactNode }) {
  const access = useAuth();
  if (access.data === null) {
    return (
      <main className="first-setup">
        <Failure error={access.error} />
      </main>
    );
  }
  if (needsFirstSetup(access.data)) {
    return <FirstSetup state={access.data} onConfigured={access.reload} />;
  }
  return children;
}

// Le compte créé, le front se présente avec ses identifiants : le navigateur
// les retient. En mode mandataire, la requête est déjà passée par lui. Puis
// la première étape de l'installation : la ligne qui installe l'agent de
// cette machine, tant qu'elle n'en a jamais eu ; sinon l'interface s'ouvre.
function FirstSetup({ state, onConfigured }: { state: AuthState; onConfigured: () => void }) {
  const t = useT();
  const action = useAction();
  const [mode, setMode] = useState<(typeof setupModes)[number]>("basic");
  const [agentLine, setAgentLine] = useState<TokenResponse | null>(null);

  const showAgentLineOrOpen = (line: TokenResponse | null) => {
    if (line === null) {
      onConfigured();
      return;
    }
    setAgentLine(line);
  };
  const createAccount = (username: string, password: string) => {
    action.run(
      async () => {
        await enableAuth({ username, password });
        await signIn(username, password);
        return agentLineOrNull();
      },
      { onDone: showAgentLineOrOpen },
    );
  };
  const trustProxy = (header: string) => {
    action.run(
      async () => {
        await enableAuth({ mode: "forward", header });
        return agentLineOrNull();
      },
      { onDone: showAgentLineOrOpen },
    );
  };

  if (agentLine !== null) {
    return <AgentLine line={agentLine} onContinue={onConfigured} />;
  }

  return (
    <main className="first-setup">
      <div className="first-setup-column">
        <Brand />
        <Card className="form-card">
          <CardHeader title={t("auth.setup_title")} />
          <CardBody>
            <p>{t("auth.setup_intro")}</p>
            <p className="secondary">{t("auth.setup_rescue")}</p>
            <Field label={t("auth.mode_label")} htmlFor="auth-mode">
              <EnumSelect id="auth-mode" value={mode} values={setupModes} labelKey="auth.mode_" onChange={setMode} />
            </Field>
            <Failure error={action.error} />
          </CardBody>
          {mode === "basic" && <AccessCredentialsForm minLength={state.password_min_length} busy={action.busy} onSubmit={createAccount} />}
          {mode === "forward" && <AccessForwardForm busy={action.busy} onSubmit={trustProxy} />}
        </Card>
      </div>
    </main>
  );
}

// La ligne de l'agent de cette machine, ou rien : elle a déjà eu un agent
// (409), ou la ligne ne s'obtient pas. Le compte est créé quoi qu'il arrive,
// l'interface doit pouvoir s'ouvrir ; la fiche de la machine redonne la
// ligne (Ré-enrôler).
async function agentLineOrNull(): Promise<TokenResponse | null> {
  try {
    return await offerLocalEnrollment();
  } catch {
    return null;
  }
}

// La deuxième étape : la ligne qui installe l'agent de la machine où tourne
// le serveur, à coller sur elle. Elle ne s'affiche qu'ici, une fois.
function AgentLine({ line, onContinue }: { line: TokenResponse; onContinue: () => void }) {
  const t = useT();
  const expiresIn = formatRemaining(t, Date.parse(line.expires_at) - Date.now());
  return (
    <main className="first-setup">
      <div className="first-setup-column">
        <Brand />
        <Card className="form-card">
          <CardHeader title={t("auth.setup_agent_title")} />
          <CardBody>
            <p>{t("auth.setup_agent_intro")}</p>
            <Note tone="warn">{t("machine.token_once", expiresIn)}</Note>
            <div className="field">
              <span className="field-label">{t("machine.prepare_label")}</span>
              <TokenBox value={line.preparation} lines />
              <span className="field-help">{t("machine.prepare_help")}</span>
            </div>
            <Note tone="warn">{t("machine.prepare_docker_note")}</Note>
            <div className="field">
              <span className="field-label">{t("machine.command_label")}</span>
              <TokenBox value={line.command} />
            </div>
            {line.url_local && <Note tone="danger">{t("machine.url_local")}</Note>}
            <p className="secondary">{t("auth.setup_agent_units")}</p>
            <p className="secondary">{t("auth.setup_agent_later")}</p>
            <div className="cluster">
              <Button onClick={onContinue}>{t("auth.setup_continue")}</Button>
            </div>
          </CardBody>
        </Card>
      </div>
    </main>
  );
}
