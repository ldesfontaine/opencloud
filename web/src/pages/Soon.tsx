import { Card, Empty } from "../components/Card";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useT } from "../i18n/context";

// Page d'attente d'une fonctionnalité à venir, sous l'entrée de menu donnée.
export function Soon({ navKey }: { navKey: string }) {
  const t = useT();
  return (
    <>
      <PageHead title={t(`nav.${navKey}`)} subtitle={t("soon.subtitle")} />
      <Card>
        <Empty
          text={t("soon.text")}
          action={
            <Pill tone="neutral" dot={false}>
              {t("soon.badge")}
            </Pill>
          }
        />
      </Card>
    </>
  );
}
