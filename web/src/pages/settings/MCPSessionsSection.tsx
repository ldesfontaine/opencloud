import { deleteMCPSession, type MCPSession } from "../../api/mcp";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, Empty } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { ago, formatClock } from "../../lib/time";

// Les sessions ouvertes par le client OAuth, à révoquer une à une.
export function MCPSessionsSection({ sessions, reload, now }: { sessions: MCPSession[]; reload: () => void; now: number }) {
  const t = useT();
  const action = useAction();
  const revoke = (session: MCPSession) => {
    action.run(() => deleteMCPSession(session.id), { confirm: t("mcp.session_revoke_confirm"), onDone: reload });
  };
  return (
    <Card className="form-card">
      <CardHeader title={t("mcp.sessions_title")} />
      <CardBody>
        <p className="secondary">{t("mcp.sessions_text")}</p>
        <Failure error={action.error} />
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
