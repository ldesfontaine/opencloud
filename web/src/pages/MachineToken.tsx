import { Navigate, useLocation } from "react-router";

import type { TokenResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, Note } from "../components/Card";
import { TokenBox } from "../components/Copy";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { formatRemaining } from "../lib/time";

// Le jeton ne vit que dans l'état de navigation : rechargée, la page n'a
// plus rien à montrer et renvoie aux machines. C'est voulu.
export function MachineToken() {
  const t = useT();
  const token = useLocation().state as TokenResponse | null;
  if (!token) {
    return <Navigate to="/machines" replace />;
  }
  const expiresIn = formatRemaining(t, Date.parse(token.expires_at) - Date.now());
  return (
    <>
      <PageHead title={t("machine.token_title")} subtitle={t("machine.token_subtitle", token.name)} />
      <Card className="form-card">
        <CardBody>
          <Note tone="warn">{t("machine.token_once", expiresIn)}</Note>
          <div className="field">
            <span className="field-label">{t("machine.token_label")}</span>
            <TokenBox value={token.token} />
          </div>
          <div className="field">
            <span className="field-label">{t("machine.command_label")}</span>
            <TokenBox value={token.command} />
          </div>
          {token.url_local && <Note tone="danger">{t("machine.url_local")}</Note>}
          <div className="cluster">
            <Button to="/machines">{t("machine.back_to_list")}</Button>
          </div>
        </CardBody>
      </Card>
    </>
  );
}
