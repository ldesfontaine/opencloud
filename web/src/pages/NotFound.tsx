import { Button } from "../components/Button";
import { EmptyCard } from "../components/Card";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";

export function NotFound() {
  const t = useT();
  return (
    <>
      <PageHead title={t("notfound.title")} subtitle={t("notfound.subtitle")} />
      <EmptyCard icon="search" title={t("notfound.title")} text={t("notfound.text")} action={<Button to="/">{t("notfound.back")}</Button>} />
    </>
  );
}
