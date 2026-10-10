import { useEffect, useState, type FormEvent } from "react";

import { api, errorKey } from "../api/client";
import type { AuthState, DiskSettings, MCPSecretResponse, MCPSession, MCPState, MCPToken, MCPTokenResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { TokenBox } from "../components/Copy";
import { Failure } from "../components/Failure";
import { Field, Input } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useI18n, useT } from "../i18n/context";
import { diskSettingsError } from "../lib/disks";
import { passwordFormError } from "../lib/auth";
import { claudeCodeCommand, lifetimesOf, redirectLines } from "../lib/mcp";
import { ago, formatClock } from "../lib/time";
import "./StatusAdmin.scss";

// Paramètres : l'accès à l'interface, les seuils d'alerte du disque, puis
// l'accès d'un agent IA. Un secret ou un
// jeton ne s'affiche qu'une fois, à sa création, dans l'état de la page ;
// rechargée, elle ne le montre plus.
export function Settings() {
  const t = useT();
  const state = useResource<MCPState>("/api/mcp");
  const access = useResource<AuthState>("/api/auth");
  const disks = useResource<DiskSettings>("/api/settings/disk");
  if (state.error) {
    return (
      <>
        <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
        <Failure error={state.error} />
      </>
    );
  }
  if (!state.data) {
    return <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />;
  }
  return (
    <>
      <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      {access.data !== null && <AccessSection state={access.data} />}
      {disks.data !== null && <DiskSection settings={disks.data} reload={disks.reload} />}
      <MCPSection state={state.data} reload={state.reload} />
    </>
  );
}

// L'accès à l'interface. Activer ou changer le mot de passe rend caducs
// les identifiants du navigateur : la page se recharge, et le navigateur
// les demande dans sa propre fenêtre.
function AccessSection({ state }: { state: AuthState }) {
  const t = useT();
  const now = useNow();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const enabled = state.mode !== "none";

  const run = (action: Promise<unknown>) => {
    setBusy(true);
    setError(null);
    action
      .then(() => window.location.reload())
      .catch((failure: unknown) => {
        setError(errorKey(failure));
        setBusy(false);
      });
  };
  const disable = () => {
    if (window.confirm(t("auth.disable_confirm"))) {
      run(api.post("/api/auth/actions/disable", {}));
    }
  };

  return (
    <Card className="form-card">
      <CardHeader
        title={t("auth.title")}
        aside={<Pill tone={enabled ? "ok" : "warn"}>{t(enabled ? "auth.state_basic" : "auth.state_none")}</Pill>}
      />
      <CardBody>
        <p>{t("auth.intro")}</p>
        {!enabled && <Note tone="warn">{t("auth.open_text")}</Note>}
        {enabled && (
          <>
            <KeyValue label={t("auth.username_label")} value={state.username} mono />
            <div className="cluster">
              <span className="secondary">{state.enabled_at !== null && t("auth.enabled_since", ago(t, state.enabled_at, now))}</span>
              <Button variant="danger" icon="x" onClick={disable} disabled={busy}>
                {t("auth.disable")}
              </Button>
            </div>
          </>
        )}
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
      </CardBody>
      <CredentialsForm
        withUsername={!enabled}
        minLength={state.password_min_length}
        busy={busy}
        onSubmit={(username, password) =>
          run(enabled ? api.put("/api/auth/password", { password }) : api.post("/api/auth/actions/enable", { username, password }))
        }
      />
    </Card>
  );
}

// Le formulaire d'activation (identifiant, mot de passe, confirmation) ou
// de changement (mot de passe, confirmation).
function CredentialsForm({
  withUsername,
  minLength,
  busy,
  onSubmit,
}: {
  withUsername: boolean;
  minLength: number;
  busy: boolean;
  onSubmit: (username: string, password: string) => void;
}) {
  const t = useT();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [error, setError] = useState<string | null>(null);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    const refusal = passwordFormError(password, confirmation, minLength);
    setError(refusal);
    if (refusal === null) {
      onSubmit(username, password);
    }
  };
  return (
    <form className="card-b" onSubmit={submit}>
      <p className="field-label">{t(withUsername ? "auth.enable_title" : "auth.change_title")}</p>
      {withUsername && (
        <Field label={t("auth.username_label")} htmlFor="auth-username" help={t("auth.username_help")}>
          <Input id="auth-username" type="text" value={username} onChange={(event) => setUsername(event.target.value)} maxLength={64} autoComplete="username" required />
        </Field>
      )}
      <Field label={t("auth.password_label")} htmlFor="auth-password" help={t("auth.password_help", minLength)}>
        <Input id="auth-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="new-password" required />
      </Field>
      <Field label={t("auth.confirm_label")} htmlFor="auth-confirm">
        <Input id="auth-confirm" type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} autoComplete="new-password" required />
      </Field>
      <p className="secondary">{t("auth.browser_prompt")}</p>
      {error !== null && <Pill tone="danger">{t(error)}</Pill>}
      <div className="cluster">
        <Button type="submit" variant="primary" icon={withUsername ? "shield" : "check"} disabled={busy}>
          {t(withUsername ? "auth.enable" : "auth.change")}
        </Button>
      </div>
    </form>
  );
}

// Les seuils d'alerte du disque. Le retour n'est pas un champ : il suit
// l'attention, cinq points dessous, et la carte le dit.
function DiskSection({ settings, reload }: { settings: DiskSettings; reload: () => void }) {
  const t = useT();
  const { reloadSession } = useI18n();
  const [attention, setAttention] = useState(String(settings.attention));
  const [danger, setDanger] = useState(String(settings.danger));
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setAttention(String(settings.attention));
    setDanger(String(settings.danger));
  }, [settings.attention, settings.danger]);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setSaved(false);
    const thresholds = { attention: Number(attention), danger: Number(danger) };
    const refusal = diskSettingsError(thresholds, settings);
    setError(refusal);
    if (refusal !== null) {
      return;
    }
    setBusy(true);
    api
      .put<DiskSettings>("/api/settings/disk", thresholds)
      .then(() => {
        setSaved(true);
        reload();
        return reloadSession();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };
  const help = t("settings.disk_percent_help", settings.min_attention, settings.max_danger);
  return (
    <Card className="form-card">
      <CardHeader title={t("settings.disk_title")} />
      <form className="card-b" onSubmit={submit}>
        <p className="secondary">{t("settings.disk_intro")}</p>
        <Field label={t("settings.disk_attention_label")} htmlFor="disk-attention" help={help}>
          <Input id="disk-attention" type="number" inputMode="numeric" min={settings.min_attention} max={settings.max_danger - 1} value={attention} onChange={(event) => setAttention(event.target.value)} required />
        </Field>
        <Field label={t("settings.disk_danger_label")} htmlFor="disk-danger">
          <Input id="disk-danger" type="number" inputMode="numeric" min={settings.min_attention + 1} max={settings.max_danger} value={danger} onChange={(event) => setDanger(event.target.value)} required />
        </Field>
        <p className="secondary">{t("settings.disk_recovery", settings.recovery)}</p>
        <p className="secondary">{t("settings.disk_applies")}</p>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy}>
            {t("settings.disk_save")}
          </Button>
          {saved && <Pill tone="ok">{t("settings.disk_saved")}</Pill>}
        </div>
      </form>
    </Card>
  );
}

function MCPSection({ state, reload }: { state: MCPState; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const [secret, setSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const run = (action: Promise<unknown>) => {
    setBusy(true);
    setError(null);
    action.catch((failure: unknown) => setError(errorKey(failure))).finally(() => {
      setBusy(false);
      reload();
    });
  };
  const enable = () => run(api.post<MCPSecretResponse>("/api/mcp/actions/enable", {}).then((response) => setSecret(response.secret)));
  const regenerate = () => {
    if (window.confirm(t("mcp.regenerate_confirm"))) {
      run(api.post<MCPSecretResponse>("/api/mcp/actions/regenerate-secret", {}).then((response) => setSecret(response.secret)));
    }
  };
  const disable = () => {
    if (window.confirm(t("mcp.disable_confirm"))) {
      setSecret(null);
      run(api.post("/api/mcp/actions/disable", {}));
    }
  };

  return (
    <>
      <Card className="form-card">
        <CardHeader
          title={t("mcp.title")}
          aside={
            <Pill tone={state.enabled ? "ok" : "neutral"}>{t(state.enabled ? "mcp.state_enabled" : "mcp.state_disabled")}</Pill>
          }
        />
        <CardBody>
          <p>{t("mcp.intro")}</p>
          <p className="secondary">{t("mcp.tools", state.tools.reads, state.tools.writes)}</p>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          {!state.enabled && (
            <>
              <p className="secondary">{t("mcp.enable_text")}</p>
              <div className="cluster">
                <Button variant="primary" icon="check" onClick={enable} disabled={busy}>
                  {t("mcp.enable")}
                </Button>
              </div>
            </>
          )}
          {state.enabled && (
            <div className="cluster">
              <span className="secondary">{state.enabled_at !== null && t("mcp.enabled_since", ago(t, state.enabled_at, now))}</span>
              <Button variant="danger" icon="x" onClick={disable} disabled={busy}>
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
                <Button icon="refresh" onClick={regenerate} disabled={busy}>
                  {t("mcp.regenerate")}
                </Button>
              </div>
            </CardBody>
          </Card>
          <RedirectURIs state={state} reload={reload} />
          <Tokens tokens={state.tokens} limit={state.limits.tokens} reload={reload} now={now} />
          <Sessions sessions={state.sessions} reload={reload} now={now} />
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
                <TokenBox value={claudeCodeCommand(state.stdio_command)} />
              </div>
            </CardBody>
          </Card>
        </>
      )}
    </>
  );
}

function RedirectURIs({ state, reload }: { state: MCPState; reload: () => void }) {
  const t = useT();
  const [text, setText] = useState(state.redirect_uris.join("\n"));
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => setText(state.redirect_uris.join("\n")), [state.redirect_uris]);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setSaved(false);
    api
      .put<MCPState>("/api/mcp/redirect-uris", { redirect_uris: redirectLines(text) })
      .then(() => {
        setError(null);
        setSaved(true);
        reload();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };
  return (
    <Card className="form-card">
      <CardHeader title={t("mcp.redirect_title")} />
      <form className="card-b" onSubmit={submit}>
        <p className="secondary">{t("mcp.redirect_text")}</p>
        <Field label={t("mcp.redirect_label")} htmlFor="mcp-redirects" help={t("mcp.redirect_help")}>
          <textarea id="mcp-redirects" className="input textarea mono" value={text} onChange={(event) => setText(event.target.value)} rows={3} />
        </Field>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy}>
            {t("mcp.save")}
          </Button>
          {saved && <Pill tone="ok">{t("mcp.saved")}</Pill>}
        </div>
      </form>
    </Card>
  );
}

function Tokens({ tokens, limit, reload, now }: { tokens: MCPToken[]; limit: number; reload: () => void; now: number }) {
  const t = useT();
  const [name, setName] = useState("");
  const [created, setCreated] = useState<MCPTokenResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<MCPTokenResponse>("/api/mcp/tokens", { name })
      .then((response) => {
        setError(null);
        setCreated(response);
        setName("");
        reload();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };
  const revoke = (token: MCPToken) => {
    if (!window.confirm(t("mcp.revoke_confirm", token.name))) {
      return;
    }
    if (created?.item.id === token.id) {
      setCreated(null);
    }
    api
      .delete(`/api/mcp/tokens/${token.id}`)
      .then(reload)
      .catch((failure: unknown) => setError(errorKey(failure)));
  };
  return (
    <Card className="form-card">
      <CardHeader title={t("mcp.tokens_title")} />
      <CardBody>
        <p className="secondary">{t("mcp.tokens_text")}</p>
      </CardBody>
      {created !== null && (
        <CardBody>
          <Note tone="warn">{t("mcp.token_once")}</Note>
          <div className="field">
            <span className="field-label">
              {t("mcp.token_label")} · {created.item.name}
            </span>
            <TokenBox value={created.token} />
          </div>
        </CardBody>
      )}
      {tokens.length === 0 ? (
        <Empty text={t("mcp.tokens_empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("mcp.col_token")}</th>
              <th>{t("mcp.col_created")}</th>
              <th>{t("mcp.col_last_used")}</th>
              <th className="th-end" />
            </tr>
          </thead>
          <tbody>
            {tokens.map((token) => (
              <tr key={token.id}>
                <td>
                  {token.name} <span className="mono secondary">{token.masked}</span>
                </td>
                <td className="secondary">{formatClock(token.created_at)}</td>
                <td className="secondary">{token.last_used_at === null ? t("mcp.never_used") : ago(t, token.last_used_at, now)}</td>
                <td className="td-end">
                  <Button variant="ghost" small onClick={() => revoke(token)}>
                    {t("mcp.revoke")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {tokens.length < limit && (
        <form className="card-b" onSubmit={submit}>
          <Field label={t("mcp.token_name_label")} htmlFor="mcp-token-name" help={t("mcp.token_name_help")}>
            <Input id="mcp-token-name" type="text" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} autoComplete="off" required />
          </Field>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon="plus" disabled={busy}>
              {t("mcp.create_token")}
            </Button>
          </div>
        </form>
      )}
    </Card>
  );
}

function Sessions({ sessions, reload, now }: { sessions: MCPSession[]; reload: () => void; now: number }) {
  const t = useT();
  const [error, setError] = useState<string | null>(null);
  const revoke = (session: MCPSession) => {
    if (!window.confirm(t("mcp.session_revoke_confirm"))) {
      return;
    }
    api
      .delete(`/api/mcp/sessions/${session.id}`)
      .then(reload)
      .catch((failure: unknown) => setError(errorKey(failure)));
  };
  return (
    <Card className="form-card">
      <CardHeader title={t("mcp.sessions_title")} />
      <CardBody>
        <p className="secondary">{t("mcp.sessions_text")}</p>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
      </CardBody>
      {sessions.length === 0 ? (
        <Empty text={t("mcp.sessions_empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("mcp.col_session")}</th>
              <th>{t("mcp.col_started")}</th>
              <th>{t("mcp.col_last_used")}</th>
              <th>{t("mcp.col_expires")}</th>
              <th className="th-end" />
            </tr>
          </thead>
          <tbody>
            {sessions.map((session) => (
              <tr key={session.id}>
                <td className="mono">{session.id}</td>
                <td className="secondary">{formatClock(session.started_at)}</td>
                <td className="secondary">{session.last_used_at === null ? t("mcp.never_used") : ago(t, session.last_used_at, now)}</td>
                <td className="secondary">{formatClock(session.expires_at)}</td>
                <td className="td-end">
                  <Button variant="ghost" small onClick={() => revoke(session)}>
                    {t("mcp.session_revoke")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}
