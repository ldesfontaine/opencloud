import { type CopyState, useCopy } from "../hooks/useCopy";
import { useT } from "../i18n/context";
import { Button } from "./Button";

const labels: Record<CopyState, string> = {
  idle: "machine.copy",
  copied: "machine.copied",
  failed: "machine.copy_failed",
};

export function CopyButton({ text, ghost = false }: { text: string; ghost?: boolean }) {
  const t = useT();
  const { state, copy } = useCopy();
  return (
    <Button variant={ghost ? "ghost" : "secondary"} small onClick={() => copy(text)}>
      {t(labels[state])}
    </Button>
  );
}

// Un texte en mono avec son bouton pour le copier. `lines` : plusieurs
// commandes, une par ligne, que le bloc fait défiler plutôt que de les couper.
export function TokenBox({ value, lines = false }: { value: string; lines?: boolean }) {
  return (
    <div className="token-box">
      {lines ? <pre className="mono">{value}</pre> : <code className="mono">{value}</code>}
      <CopyButton text={value} />
    </div>
  );
}
