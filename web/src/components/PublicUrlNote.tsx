import { useT } from "../i18n/context";
import { Note } from "./Card";
import { TokenBox } from "./Copy";

// Ce que dit un écran qui écrit une adresse d'{product} (commande d'une
// machine, URL de ping) quand cette adresse paraît locale. Un essai local
// est prévu : la note est neutre et donne la ligne à poser. Elle ne vire au
// rouge que si la page, elle, est publique (suggestion non vide) : c'est un
// public_url mal réglé, et la ligne porte alors l'adresse de la page.
export function PublicUrlNote({ local, suggestion }: { local: boolean; suggestion: string }) {
  const t = useT();
  if (!local) {
    return null;
  }
  const mismatch = suggestion !== "";
  return (
    <Note tone={mismatch ? "danger" : "neutral"}>
      <p>{t(mismatch ? "public_url.mismatch_note" : "public_url.local_note")}</p>
      <div className="stack">
        <TokenBox value={`public_url = "${mismatch ? suggestion : "https://…"}"`} />
        <span className="secondary">{t("public_url.restart_label")}</span>
        <TokenBox value={t("public_url.restart_command")} />
      </div>
    </Note>
  );
}
