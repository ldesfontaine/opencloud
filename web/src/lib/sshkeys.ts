import type { SSHKey, SSHReport } from "../api/sshkeys";

// Le seuil sous lequel une clé RSA est tenue pour faible.
const minRSABits = 3072;

// Une clé est faible quand elle est DSA, ou RSA sous 3072 bits.
export function isWeak(key: Pick<SSHKey, "type" | "bits">): boolean {
  return key.type === "ssh-dss" || (key.type === "ssh-rsa" && key.bits < minRSABits);
}

// L'empreinte sans son préfixe, raccourcie : de quoi reconnaître une clé d'un
// coup d'œil, le titre de la cellule portant l'empreinte entière.
export function shortFingerprint(fingerprint: string): string {
  const body = fingerprint.startsWith("SHA256:") ? fingerprint.slice("SHA256:".length) : fingerprint;
  return body.length > 12 ? `${body.slice(0, 12)}…` : body;
}

// Une cellule de la matrice : ce que cette machine dit d'une clé.
export type Cell = { kind: "accounts"; accounts: string[] } | { kind: "none" } | { kind: "unknown" };

export interface MatrixRow {
  fingerprint: string;
  type: string;
  bits: number;
  // Le premier commentaire non vide rencontré : une même clé en porte souvent plusieurs.
  comment: string;
  weak: boolean;
  cells: Cell[];
}

// Les clés distinctes de toutes les machines, une ligne par empreinte, une
// cellule par machine (dans l'ordre de `machines`) :
// - les comptes qui l'acceptent, quand la machine est relevée ;
// - rien (« — »), quand elle l'est sans cette clé et que tous ses comptes sont lus ;
// - inconnu (« ? »), quand la machine n'est pas relevée, ou qu'un de ses comptes
//   n'a pas pu être lu : l'absence n'y prouve rien.
export function buildMatrix(machines: SSHReport[]): MatrixRow[] {
  const rows = new Map<string, MatrixRow>();
  for (const machine of machines) {
    for (const account of machine.accounts) {
      for (const key of account.keys) {
        if (!rows.has(key.fingerprint)) {
          rows.set(key.fingerprint, { fingerprint: key.fingerprint, type: key.type, bits: key.bits, comment: "", weak: isWeak(key), cells: [] });
        }
        const row = rows.get(key.fingerprint);
        if (row !== undefined && row.comment === "" && key.comment !== "") {
          row.comment = key.comment;
        }
      }
    }
  }
  const ordered = [...rows.values()].sort((a, b) => a.comment.localeCompare(b.comment) || a.fingerprint.localeCompare(b.fingerprint));
  for (const row of ordered) {
    row.cells = machines.map((machine) => cellOf(machine, row.fingerprint));
  }
  return ordered;
}

function cellOf(machine: SSHReport, fingerprint: string): Cell {
  const accounts = machine.accounts.filter((account) => account.keys.some((key) => key.fingerprint === fingerprint)).map((account) => account.name);
  if (accounts.length > 0) {
    return { kind: "accounts", accounts };
  }
  if (machine.reported_at === null || machine.truncated || machine.accounts.some((account) => account.state === "denied" || account.state === "unsafe" || account.state === "too_large" || account.truncated)) {
    return { kind: "unknown" };
  }
  return { kind: "none" };
}

// Vrai quand au moins un compte de la machine n'a pas pu être lu parce que
// l'agent n'en avait pas le droit : la phrase sur les comptes dédiés se justifie.
export function hasDenied(report: SSHReport): boolean {
  return report.accounts.some((account) => account.state === "denied");
}
