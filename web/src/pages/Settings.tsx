import { useAPITokens } from "../api/apiTokens";
import { useAuth } from "../api/auth";
import { useMCPCheck } from "../api/mcpCheck";
import { useDiskSettings, useLifecycleSettings, useMailSettings } from "../api/settings";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { AccessSection } from "./settings/AccessSection";
import { AIAgentSection } from "./settings/AIAgentSection";
import { APITokensSection } from "./settings/APITokensSection";
import { DiskSection } from "./settings/DiskSection";
import { LifecycleSection } from "./settings/LifecycleSection";
import { MailSection } from "./settings/MailSection";

// Paramètres : l'accès à l'interface, les seuils d'alerte du disque, le
// relais d'e-mails, la fin de support, puis l'agent IA (mcp-maison) et les
// jetons d'API. Chaque section lit la sienne : l'une qui échoue ne retient
// pas les autres. Un jeton ne s'affiche qu'une fois, à sa création, dans
// l'état de sa section ; rechargée, elle ne le montre plus.
export function Settings() {
  const t = useT();
  const access = useAuth();
  const disks = useDiskSettings();
  const mail = useMailSettings();
  const support = useLifecycleSettings();
  const aiAgent = useMCPCheck();
  const tokens = useAPITokens();
  return (
    <>
      <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      {access.data !== null && <AccessSection state={access.data} />}
      {disks.data !== null && <DiskSection settings={disks.data} reload={disks.reload} />}
      {mail.data !== null && <MailSection settings={mail.data} reload={mail.reload} />}
      {support.data !== null && <LifecycleSection settings={support.data} reload={support.reload} />}
      {aiAgent.data !== null && <AIAgentSection check={aiAgent.data} reload={aiAgent.reload} />}
      {tokens.data !== null && <APITokensSection state={tokens.data} reload={tokens.reload} />}
    </>
  );
}
