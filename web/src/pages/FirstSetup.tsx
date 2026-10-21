import { useState, type ReactNode } from "react";

import { enableAuth, signIn, useAuth, type AuthState } from "../api/auth";
import { Card, CardBody, CardHeader } from "../components/Card";
import { Failure } from "../components/Failure";
import { EnumSelect, Field } from "../components/Field";
import { Brand } from "../components/Layout";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { needsFirstSetup } from "../lib/auth";
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
// les retient et l'interface s'ouvre sans fenêtre de connexion. En mode
// mandataire, la requête est déjà passée par lui : il suffit de relire l'accès.
function FirstSetup({ state, onConfigured }: { state: AuthState; onConfigured: () => void }) {
  const t = useT();
  const action = useAction();
  const [mode, setMode] = useState<(typeof setupModes)[number]>("basic");

  const createAccount = (username: string, password: string) => {
    action.run(
      async () => {
        await enableAuth({ username, password });
        await signIn(username, password);
      },
      { onDone: onConfigured },
    );
  };
  const trustProxy = (header: string) => {
    action.run(() => enableAuth({ mode: "forward", header }), { onDone: onConfigured });
  };

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
          {mode === "basic" && <AccessCredentialsForm withUsername minLength={state.password_min_length} busy={action.busy} onSubmit={createAccount} />}
          {mode === "forward" && <AccessForwardForm busy={action.busy} onSubmit={trustProxy} />}
        </Card>
      </div>
    </main>
  );
}
