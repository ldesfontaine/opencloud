import type { APITokensState } from "../../api/apiTokens";
import type { MCPCheck, MCPCheckState } from "../../api/mcpCheck";
import { Button } from "../../components/Button";
import { TokenBox } from "../../components/Copy";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingRow } from "../../components/SettingRow";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import type { Tone } from "../../lib/tone";
import { ago } from "../../lib/time";
import { APITokensBlock } from "./APITokensBlock";
import { SettingsSection } from "./SettingsSection";

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

// L'agent IA passe par mcp-maison, installé à côté : la section dit ce qu'il
// lit et ce qu'il ne fait jamais, si le /health de mcp-maison répond, puis
// porte les jetons d'API qui lui ouvrent l'API. Chaque moitié lit sa
// ressource : l'une qui échoue ne retient pas l'autre. Rien n'est vérifié en
// fond : la ligne relit à l'ouverture et au bouton.
export function AIAgentSection({
  check,
  reloadCheck,
  tokens,
  reloadTokens,
}: {
  check: MCPCheck | null;
  reloadCheck: () => void;
  tokens: APITokensState | null;
  reloadTokens: () => void;
}) {
  const t = useT();
  return (
    <SettingsSection id="agent-ia" titleKey="settings.section_ai" text={t("ai_agent.intro")}>
      {check !== null && <MCPCheckRows check={check} reload={reloadCheck} />}
      {tokens !== null && <APITokensBlock state={tokens} reload={reloadTokens} />}
    </SettingsSection>
  );
}

function MCPCheckRows({ check, reload }: { check: MCPCheck; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const reachable = check.state === "reachable";
  return (
    <>
      <SettingBlock>
        <p className="secondary">{t("ai_agent.never")}</p>
      </SettingBlock>
      <SettingRow label={t("ai_agent.title")} description={t("ai_agent.checked_at", ago(t, check.checked_at, now))}>
        <Pill tone={tones[check.state]}>{t(`ai_agent.state_${check.state}`)}</Pill>
        <Button icon="refresh" onClick={reload}>
          {t("ai_agent.check")}
        </Button>
      </SettingRow>
      {check.url !== "" && (
        <SettingRow label={t("ai_agent.url_label")}>
          <span className="mono">{check.url}</span>
        </SettingRow>
      )}
      {reachable && (
        <SettingRow label={t("ai_agent.version_label")} description={t("ai_agent.tools", check.opencloud_tools)}>
          <span className="mono">{check.version === "" ? t("ai_agent.version_unknown") : check.version}</span>
        </SettingRow>
      )}
      {!reachable && (
        <SettingBlock>
          <p className="secondary">{t(`ai_agent.hint_${check.state}`)}</p>
        </SettingBlock>
      )}
      {check.state === "not_configured" && (
        <SettingRow label={t("ai_agent.suggest")} block>
          <TokenBox value={`mcp_url = "${check.suggested_url}"`} />
        </SettingRow>
      )}
    </>
  );
}
