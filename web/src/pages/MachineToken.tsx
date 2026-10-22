import { useEffect, useState } from "react";
import { Navigate, useLocation, useNavigate } from "react-router";

import { Button } from "../components/Button";
import { Card, CardBody, Note } from "../components/Card";
import { CopyButton, TokenBox } from "../components/Copy";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { unitDropIn, unitWritePaths, type TokenPageState } from "../lib/machines";
import { formatRemaining } from "../lib/time";

// Le jeton arrive par l'état de navigation et en sort aussitôt : le
// navigateur garde cet état au rechargement et au retour arrière, et le
// jeton ne doit s'afficher qu'une fois. Rechargée, la page n'a plus rien à
// montrer et renvoie aux machines. C'est voulu.
export function MachineToken() {
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const [token] = useState(location.state as TokenPageState | null);
  useEffect(() => {
    if (location.state !== null) {
      void navigate(location.pathname, { replace: true, state: null });
    }
  }, [location, navigate]);
  if (!token) {
    return <Navigate to="/machines" replace />;
  }
  // Un droit qui écrit hors de l'état de l'agent demande un drop-in de son unité.
  const writePaths = unitWritePaths(token.rights ?? []);
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
          {writePaths.length > 0 && (
            <Note tone="warn">
              <p>{t("machine.unit_read_write", writePaths.join(", "))}</p>
              <div className="token-box">
                <pre className="mono">{unitDropIn(writePaths)}</pre>
                <CopyButton text={unitDropIn(writePaths)} />
              </div>
            </Note>
          )}
          {token.url_local && <Note tone="danger">{t("machine.url_local")}</Note>}
          <div className="cluster">
            <Button to="/machines">{t("machine.back_to_list")}</Button>
          </div>
        </CardBody>
      </Card>
    </>
  );
}
