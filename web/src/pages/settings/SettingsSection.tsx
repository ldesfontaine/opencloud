import type { ReactNode } from "react";

import { Card } from "../../components/Card";
import { SectionHead } from "../../components/SectionHead";
import { useT } from "../../i18n/context";
import type { SettingsSectionId } from "../../lib/settingsSections";

// Une section de Paramètres : son ancre, son titre et sa phrase, puis sa
// carte de lignes de réglage. Le titre vient du catalogue par la clé que la
// page partage avec la navigation.
export function SettingsSection({
  id,
  titleKey,
  text,
  aside,
  children,
}: {
  id: SettingsSectionId;
  titleKey: string;
  text: string;
  aside?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  const titleId = `${id}-titre`;
  return (
    <section className="settings-section" id={id} aria-labelledby={titleId}>
      <SectionHead id={titleId} title={t(titleKey)} text={text} aside={aside} />
      <Card className="setting-stack">{children}</Card>
    </section>
  );
}
