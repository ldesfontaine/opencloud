import { useState } from "react";

import { createAPIToken, deleteAPIToken, type APIToken, type APITokenResponse, type APITokensState } from "../../api/apiTokens";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, Empty, Note } from "../../components/Card";
import { CopyButton } from "../../components/Copy";
import { Failure } from "../../components/Failure";
import { Field, Input, Select } from "../../components/Field";
import { FormActions, FormBody } from "../../components/FormCard";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { emptyTokenForm, envBlock, expiryLabel, lifetimeLabel, tokenRequestOf } from "../../lib/apiTokens";
import { ago, formatClock } from "../../lib/time";

// Les jetons d'API : la liste à révoquer, et le formulaire qui en crée un.
// Le jeton neuf ne se montre qu'une fois, avec les lignes du .env de
// mcp-maison, dans l'état de la section ; relue, elle ne montre que le masque.
export function APITokensSection({ state, reload }: { state: APITokensState; reload: () => void }) {
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
    revoking.run(perform, { confirm: t("api_token.revoke_confirm", token.name), onDone: reload });
  };
  const lines = created === null ? "" : envBlock(state.env, created.token);
  return (
    <Card className="form-card-wide">
      <CardHeader title={t("api_token.title")} />
      <CardBody>
        <p className="secondary">{t("api_token.text")}</p>
        <Failure error={revoking.error} />
      </CardBody>
      {created !== null && (
        <CardBody>
          <Note tone="warn">{t("api_token.once")}</Note>
          <div className="kv">
            <span className="field-label">
              {t("api_token.env_label")} · {created.item.name}
            </span>
            <CopyButton text={lines} ghost />
          </div>
          <pre className="snippet mono">{lines}</pre>
        </CardBody>
      )}
      {state.tokens.length === 0 ? (
        <Empty text={t("api_token.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("api_token.col_token")}</th>
              <th>{t("api_token.col_created")}</th>
              <th>{t("api_token.col_expires")}</th>
              <th>{t("api_token.col_last_used")}</th>
              <th className="th-end" />
            </tr>
          </thead>
          <tbody>
            {state.tokens.map((token) => (
              <tr key={token.id}>
                <td>
                  {token.name} <span className="mono secondary">{token.masked}</span>
                </td>
                <td className="secondary">{formatClock(token.created_at)}</td>
                <td className="secondary">{expiryLabel(t, token.expires_at, formatClock)}</td>
                <td className="secondary">{token.last_used_at === null ? t("api_token.never_used") : ago(t, token.last_used_at, now)}</td>
                <td className="td-end">
                  <Button variant="ghost" small onClick={() => revoke(token)}>
                    {t("api_token.revoke")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {state.tokens.length < state.limit && (
        <FormBody
          onSubmit={create}
          error={creating.error}
          actions={<FormActions submitLabel={t("api_token.create")} submitIcon="plus" busy={creating.busy} />}
        >
          <Field label={t("api_token.name_label")} htmlFor="api-token-name" help={t("api_token.name_help")}>
            <Input id="api-token-name" type="text" value={values.name} onChange={(event) => change("name", event.target.value)} maxLength={80} autoComplete="off" required />
          </Field>
          <Field label={t("api_token.lifetime_label")} htmlFor="api-token-lifetime" help={t("api_token.lifetime_help")}>
            <Select id="api-token-lifetime" value={String(values.days)} onChange={(event) => change("days", Number(event.target.value))}>
              {state.lifetimes.days.map((value) => (
                <option key={value} value={value}>
                  {lifetimeLabel(t, value)}
                </option>
              ))}
            </Select>
          </Field>
        </FormBody>
      )}
    </Card>
  );
}
