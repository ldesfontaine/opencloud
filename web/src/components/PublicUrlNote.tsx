import { useT } from "../i18n/context";
import { Note } from "./Card";
import { TokenBox } from "./Copy";

// Ce que dit un écran qui écrit une adresse d'{product} (commande d'une
// machine, URL de ping) quand cette adresse paraît locale. Un essai local
// est prévu : la note est neutre. Elle ne vire au rouge que si la page, elle,
// est publique (suggestion non vide) : c'est un public_url mal réglé, et la
// ligne à copier porte l'adresse de la page.
// Sans suggestion, l'adresse publique n'est pas connue : aucune ligne n'est à
// copier (collée telle quelle, une adresse inventée casserait la configuration),
// seulement la forme attendue, en exemple.
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
        {mismatch ? (
          <TokenBox value={`public_url = "${suggestion}"`} />
        ) : (
          <>
            <span className="secondary">{t("public_url.example_label")}</span>
            <code className="mono public-url-example">{t("public_url.example_line")}</code>
          </>
        )}
        <span className="secondary">{t("public_url.restart_label")}</span>
        <TokenBox value={t("public_url.restart_command")} />
      </div>
    </Note>
  );
}
