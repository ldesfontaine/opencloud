import type { MCPCheck, MCPCheckState } from "../../api/mcpCheck";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, KeyValue } from "../../components/Card";
import { TokenBox } from "../../components/Copy";
import { Pill } from "../../components/Pill";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import type { Tone } from "../../lib/tone";
import { ago } from "../../lib/time";

// La pastille de chaque verdict : joignable va ; sans ses outils ou une
// réponse inattendue demandent un regard ; rien qui répond est un danger ;
// non réglé n'est pas un défaut, openCloud marche sans.
const tones: Record<MCPCheckState, Tone> = {
  not_configured: "neutral",
  reachable: "ok",
  group_missing: "warn",
  unreachable: "danger",
  invalid_answer: "warn",
};

// L'agent IA passe par mcp-maison, installé à côté : la carte dit ce qu'il
// lit et ce qu'il ne fait jamais, puis si le /health de mcp-maison répond.
// Rien n'est vérifié en fond : la carte relit à son ouverture et au bouton.
export function AIAgentSection({ check, reload }: { check: MCPCheck; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const reachable = check.state === "reachable";
  return (
    <Card className="form-card">
      <CardHeader title={t("ai_agent.title")} aside={<Pill tone={tones[check.state]}>{t(`ai_agent.state_${check.state}`)}</Pill>} />
      <CardBody>
        <p>{t("ai_agent.intro")}</p>
        <p className="secondary">{t("ai_agent.never")}</p>
        {check.url !== "" && <KeyValue label={t("ai_agent.url_label")} value={check.url} mono />}
        {reachable && (
          <>
            <KeyValue label={t("ai_agent.version_label")} value={check.version === "" ? t("ai_agent.version_unknown") : check.version} mono />
            <p className="secondary">{t("ai_agent.tools", check.opencloud_tools)}</p>
          </>
        )}
        {!reachable && <p className="secondary">{t(`ai_agent.hint_${check.state}`)}</p>}
        {check.state === "not_configured" && (
          <div className="field">
            <span className="field-label">{t("ai_agent.suggest")}</span>
            <TokenBox value={`mcp_url = "${check.suggested_url}"`} />
          </div>
        )}
        <div className="cluster">
          <Button icon="refresh" onClick={reload}>
            {t("ai_agent.check")}
          </Button>
          <span className="secondary">{t("ai_agent.checked_at", ago(t, check.checked_at, now))}</span>
        </div>
      </CardBody>
    </Card>
  );
}
