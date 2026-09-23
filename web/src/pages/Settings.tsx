import { useEffect, useState, type FormEvent } from "react";

import { api, errorKey } from "../api/client";
import type { MCPSecretResponse, MCPSession, MCPState, MCPToken, MCPTokenResponse } from "../api/types";
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
import { useT } from "../i18n/context";
import { claudeCodeCommand, lifetimesOf, redirectLines } from "../lib/mcp";
import { ago, formatClock } from "../lib/time";
import "./StatusAdmin.scss";

// Paramètres : pour l'instant, l'accès d'un agent IA. Un secret ou un
// jeton ne s'affiche qu'une fois, à sa création, dans l'état de la page ;
// rechargée, elle ne le montre plus.
export function Settings() {
  const t = useT();
  const state = useResource<MCPState>("/api/mcp");
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
      <MCPSection state={state.data} reload={state.reload} />
    </>
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
  const enable = () => run(api.post<MCPSecretResponse>("/api/mcp/actions/enable").then((response) => setSecret(response.secret)));
  const regenerate = () => {
    if (window.confirm(t("mcp.regenerate_confirm"))) {
      run(api.post<MCPSecretResponse>("/api/mcp/actions/regenerate-secret").then((response) => setSecret(response.secret)));
    }
  };
  const disable = () => {
    if (window.confirm(t("mcp.disable_confirm"))) {
      setSecret(null);
      run(api.post("/api/mcp/actions/disable"));
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
            <CardHeader title={t("mcp.client_title")} aside={t("mcp.client_text")} />
            <CardBody>
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
            <CardHeader title={t("mcp.stdio_title")} aside={t("mcp.stdio_text")} />
            <CardBody>
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
      <CardHeader title={t("mcp.redirect_title")} aside={t("mcp.redirect_text")} />
      <form className="card-b" onSubmit={submit}>
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
      <CardHeader title={t("mcp.tokens_title")} aside={t("mcp.tokens_text")} />
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
      <CardHeader title={t("mcp.sessions_title")} aside={t("mcp.sessions_text")} />
      {error !== null && (
        <CardBody>
          <Pill tone="danger">{t(error)}</Pill>
        </CardBody>
      )}
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
