import { useState } from "react";

import { createAPIToken, deleteAPIToken, type APIToken, type APITokenResponse, type APITokensState } from "../../api/apiTokens";
import { Button } from "../../components/Button";
import { Note } from "../../components/Card";
import { ConfirmInline } from "../../components/ConfirmInline";
import { CopyButton } from "../../components/Copy";
import { Failure } from "../../components/Failure";
import { Input } from "../../components/Field";
import { Select } from "../../components/Select";
import { SettingBlock, SettingFoot, SettingRow } from "../../components/SettingRow";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { emptyTokenForm, envBlock, expiryLabel, lifetimeLabel, scopeLabel, toggleVerb, tokenRequestOf } from "../../lib/apiTokens";
import { ago, formatClock } from "../../lib/time";
import { VerbChoices } from "./VerbChoices";

// Les jetons d'API, sous le réglage de l'agent IA : la liste, où chaque
// jeton se révoque sur sa ligne après une confirmation en ligne, puis le
// formulaire qui en crée un. Le jeton neuf ne se montre qu'une fois, avec
// les lignes du .env de mcp-maison, dans l'état de la section ; relue, elle
// ne montre que le masque.
export function APITokensBlock({ state, reload }: { state: APITokensState; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const { values, change, update } = useForm(emptyTokenForm(state.lifetimes.default_days));
  const [created, setCreated] = useState<APITokenResponse | null>(null);
  const creating = useAction();
  const revoking = useAction();

  const create = () => {
    creating.run(() => createAPIToken(tokenRequestOf(values)), {
      onDone: (response) => {
        setCreated(response);
        update(emptyTokenForm(state.lifetimes.default_days));
        reload();
      },
    });
  };
  const revoke = (token: APIToken) => {
    const perform = () => {
      // Une fois confirmé, le jeton montré une seule fois ne se montre plus.
      if (created?.item.id === token.id) {
        setCreated(null);
      }
      return deleteAPIToken(token.id);
    };
    revoking.run(perform, { onDone: reload });
  };
  const lines = created === null ? "" : envBlock(state.env, created.token);
  const labelOfVerb = (id: string) => {
    const offer = state.verbs.find((candidate) => candidate.id === id);
    return offer === undefined ? id : t(offer.label);
  };
  return (
    <>
      <div className="setting-subhead">
        <span className="card-t">{t("api_token.title")}</span>
        <span className="secondary">{t("api_token.text")}</span>
      </div>
      {revoking.error !== null && (
        <SettingBlock>
          <Failure error={revoking.error} />
        </SettingBlock>
      )}
      {created !== null && (
        <SettingBlock>
          <Note tone="warn">{t("api_token.once")}</Note>
          <div className="kv">
            <span className="field-label">
              {t("api_token.env_label")} · {created.item.name}
            </span>
            <CopyButton text={lines} ghost />
          </div>
          <pre className="snippet mono">{lines}</pre>
        </SettingBlock>
      )}
      {state.tokens.length === 0 ? (
        <SettingBlock>
          <p className="secondary">{t("api_token.empty")}</p>
        </SettingBlock>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("api_token.col_token")}</th>
              <th>{t("api_token.col_scope")}</th>
              <th className="nowrap">{t("api_token.col_created")}</th>
              <th className="nowrap">{t("api_token.col_expires")}</th>
              <th className="nowrap">{t("api_token.col_last_used")}</th>
              <th className="th-end" />
            </tr>
          </thead>
          <tbody>
            {state.tokens.map((token) => (
              <tr key={token.id}>
                <td>
                  {token.name} <span className="mono secondary">{token.masked}</span>
                </td>
                <td className="secondary">{scopeLabel(t, token, labelOfVerb)}</td>
                <td className="secondary nowrap">{formatClock(token.created_at)}</td>
                <td className="secondary nowrap">{expiryLabel(t, token.expires_at, formatClock)}</td>
                <td className="secondary nowrap">{token.last_used_at === null ? t("api_token.never_used") : ago(t, token.last_used_at, now)}</td>
                <td className="td-end">
                  <ConfirmInline variant="ghost" small disabled={revoking.busy} question={t("api_token.revoke_confirm", token.name)} onConfirm={() => revoke(token)}>
                    {t("api_token.revoke")}
                  </ConfirmInline>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {state.tokens.length < state.limit && (
        <form
          className="setting-stack"
          onSubmit={(event) => {
            event.preventDefault();
            create();
          }}
        >
          <SettingRow label={t("api_token.name_label")} description={t("api_token.name_help")} htmlFor="api-token-name" width="medium">
            <Input id="api-token-name" type="text" value={values.name} onChange={(event) => change("name", event.target.value)} maxLength={80} autoComplete="off" required />
          </SettingRow>
          <SettingRow label={t("api_token.scope_label")} description={t("api_token.scope_help")} htmlFor="api-token-scope">
            <Select
              id="api-token-scope"
              value={values.scope}
              options={[
                { value: "read", label: t("api_token.scope_read") },
                { value: "write", label: t("api_token.scope_write") },
              ]}
              onChange={(chosen) => change("scope", chosen === "write" ? "write" : "read")}
            />
          </SettingRow>
          {values.scope === "write" && (
            <SettingBlock>
              <Note tone="warn">{t("api_token.write_warn")}</Note>
              <VerbChoices offers={state.verbs} chosen={values.verbs} onToggle={(id) => change("verbs", toggleVerb(values.verbs, id))} />
            </SettingBlock>
          )}
          <SettingRow label={t("api_token.lifetime_label")} description={t("api_token.lifetime_help")} htmlFor="api-token-lifetime">
            <Select
              id="api-token-lifetime"
              value={String(values.days)}
              options={state.lifetimes.days.map((value) => ({ value: String(value), label: lifetimeLabel(t, value) }))}
              onChange={(chosen) => change("days", Number(chosen))}
            />
          </SettingRow>
          {creating.error !== null && (
            <SettingBlock>
              <Failure error={creating.error} />
            </SettingBlock>
          )}
          <SettingFoot>
            <Button type="submit" variant="primary" icon="plus" disabled={creating.busy}>
              {t("api_token.create")}
            </Button>
          </SettingFoot>
        </form>
      )}
    </>
  );
}
