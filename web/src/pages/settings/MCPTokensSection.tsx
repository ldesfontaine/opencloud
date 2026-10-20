import { useState } from "react";

import { createMCPToken, deleteMCPToken, type MCPToken, type MCPTokenResponse } from "../../api/mcp";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, Empty, Note } from "../../components/Card";
import { TokenBox } from "../../components/Copy";
import { Failure } from "../../components/Failure";
import { Field, Input, Select } from "../../components/Field";
import { FormActions, FormBody } from "../../components/FormCard";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { emptyTokenForm, expiryLabel, lifetimeLabel, tokenRequestOf } from "../../lib/mcp";
import { ago, formatClock } from "../../lib/time";

// Les jetons d'API : la liste à révoquer, et le formulaire qui en crée un.
// Le jeton neuf ne se montre qu'une fois, dans l'état de la section.
export function MCPTokensSection({
  tokens,
  limit,
  lifetimes,
  defaultDays,
  reload,
  now,
}: {
  tokens: MCPToken[];
  limit: number;
  lifetimes: number[];
  defaultDays: number;
  reload: () => void;
  now: number;
}) {
  const t = useT();
  const { values, change, update } = useForm(emptyTokenForm(defaultDays));
  const [created, setCreated] = useState<MCPTokenResponse | null>(null);
  const creating = useAction();
  const revoking = useAction();

  const create = () => {
    creating.run(() => createMCPToken(tokenRequestOf(values)), {
      onDone: (response) => {
        setCreated(response);
        update(emptyTokenForm(defaultDays));
        reload();
      },
    });
  };
  const revoke = (token: MCPToken) => {
    const perform = () => {
      // Une fois confirmé, le jeton montré une seule fois ne se montre plus.
      if (created?.item.id === token.id) {
        setCreated(null);
      }
      return deleteMCPToken(token.id);
    };
    revoking.run(perform, { confirm: t("mcp.revoke_confirm", token.name), onDone: reload });
  };
  return (
    <Card className="form-card-wide">
      <CardHeader title={t("mcp.tokens_title")} />
      <CardBody>
        <p className="secondary">{t("mcp.tokens_text")}</p>
        <Failure error={revoking.error} />
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
              <th>{t("mcp.col_expires")}</th>
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
                <td className="secondary">{expiryLabel(t, token.expires_at, formatClock)}</td>
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
        <FormBody
          onSubmit={create}
          error={creating.error}
          actions={<FormActions submitLabel={t("mcp.create_token")} submitIcon="plus" busy={creating.busy} />}
        >
          <Field label={t("mcp.token_name_label")} htmlFor="mcp-token-name" help={t("mcp.token_name_help")}>
            <Input id="mcp-token-name" type="text" value={values.name} onChange={(event) => change("name", event.target.value)} maxLength={80} autoComplete="off" required />
          </Field>
          <Field label={t("mcp.lifetime_label")} htmlFor="mcp-token-lifetime" help={t("mcp.lifetime_help")}>
            <Select id="mcp-token-lifetime" value={String(values.days)} onChange={(event) => change("days", Number(event.target.value))}>
              {lifetimes.map((value) => (
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
