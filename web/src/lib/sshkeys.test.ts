import { expect, test } from "vitest";

import type { SSHAccount, SSHKey, SSHReport } from "../api/sshkeys";
import { buildMatrix, hasDenied, isWeak, shortFingerprint } from "./sshkeys";

const fpA = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8";
const fpB = "SHA256:Qa1b2c3d4e5f6g7h8i9j0KLMNOPQRSTUVWXYZabcdef";

function key(fingerprint: string, comment = "", over: Partial<SSHKey> = {}): SSHKey {
  return { file: "authorized_keys", fingerprint, type: "ssh-ed25519", bits: 256, comment, options: [], ...over };
}

function account(name: string, keys: SSHKey[], over: Partial<SSHAccount> = {}): SSHAccount {
  return { name, uid: 1000, state: "read", truncated: false, ignored: 0, keys, ...over };
}

function report(name: string, accounts: SSHAccount[], over: Partial<SSHReport> = {}): SSHReport {
  return { machine_id: name, machine_name: name, reported_at: "2026-10-22T10:00:00Z", truncated: false, accounts, ...over };
}

test("une clé RSA sous 3072 bits ou une clé DSA est faible, les autres non", () => {
  expect(isWeak({ type: "ssh-rsa", bits: 2048 })).toBe(true);
  expect(isWeak({ type: "ssh-rsa", bits: 3072 })).toBe(false);
  expect(isWeak({ type: "ssh-dss", bits: 1024 })).toBe(true);
  expect(isWeak({ type: "ssh-ed25519", bits: 256 })).toBe(false);
  expect(isWeak({ type: "ecdsa-sha2-nistp256", bits: 256 })).toBe(false);
});

test("l'empreinte courte perd son préfixe et se coupe", () => {
  expect(shortFingerprint(fpA)).toBe("nThbg6kXUpJW…");
  expect(shortFingerprint("SHA256:court")).toBe("court");
});

test("la matrice a une ligne par empreinte et une cellule par machine, dans l'ordre", () => {
  const bastion = report("bastion", [account("root", [key(fpA, "lucas@pc")]), account("lucas", [key(fpA)])]);
  const vps = report("vps", [account("root", [key(fpB, "vieux", { type: "ssh-rsa", bits: 2048 })])]);

  const rows = buildMatrix([bastion, vps]);

  // Triées par commentaire : « lucas@pc » avant « vieux ».
  expect(rows.map((row) => row.fingerprint)).toEqual([fpA, fpB]);
  const a = rows.find((row) => row.fingerprint === fpA);
  const b = rows.find((row) => row.fingerprint === fpB);
  expect(a?.cells).toEqual([{ kind: "accounts", accounts: ["root", "lucas"] }, { kind: "none" }]);
  expect(a?.comment).toBe("lucas@pc");
  expect(a?.weak).toBe(false);
  expect(b?.cells).toEqual([{ kind: "none" }, { kind: "accounts", accounts: ["root"] }]);
  expect(b?.weak).toBe(true);
});

test("une machine pas relevée ou en partie illisible ne prouve pas l'absence : « ? »", () => {
  const unreported = report("neuve", [], { reported_at: null });
  const partial = report("partielle", [account("root", []), account("lucas", [], { state: "denied" })]);
  const truncated = report("longue", [account("root", [])], { truncated: true });
  const full = report("lue", [account("root", [key(fpA)])]);

  const rows = buildMatrix([full, unreported, partial, truncated]);

  expect(rows).toHaveLength(1);
  expect(rows[0]?.cells).toEqual([{ kind: "accounts", accounts: ["root"] }, { kind: "unknown" }, { kind: "unknown" }, { kind: "unknown" }]);
});

test("sans aucune clé, la matrice est vide", () => {
  expect(buildMatrix([report("a", [account("root", [], { state: "none" })])])).toEqual([]);
});

test("hasDenied ne regarde que les comptes refusés", () => {
  expect(hasDenied(report("a", [account("root", []), account("x", [], { state: "denied" })]))).toBe(true);
  expect(hasDenied(report("a", [account("root", [], { state: "unsafe" })]))).toBe(false);
});
