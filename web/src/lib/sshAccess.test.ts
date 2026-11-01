import { expect, test } from "vitest";

import type { SSHAccount, SSHKey, SSHReport } from "../api/sshkeys";
import { buildKeyAccess, buildMachineAccess, filterKeys, isComplete, summarizeAccess } from "./sshAccess";

const fpA = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8";
const fpB = "SHA256:Qa1b2c3d4e5f6g7h8i9j0KLMNOPQRSTUVWXYZabcdef";
const fpC = "SHA256:Zp3sD8fVn1YcAbCdEfGhIjKlMnOpQrStUvWxYz01234";

function key(fingerprint: string, comment = "", over: Partial<SSHKey> = {}): SSHKey {
  return { file: "authorized_keys", fingerprint, type: "ssh-ed25519", bits: 256, comment, options: [], ...over };
}

function account(name: string, keys: SSHKey[], over: Partial<SSHAccount> = {}): SSHAccount {
  return { name, uid: 1000, state: "read", truncated: false, ignored: 0, keys, ...over };
}

function root(keys: SSHKey[], over: Partial<SSHAccount> = {}): SSHAccount {
  return account("root", keys, { uid: 0, ...over });
}

function report(name: string, accounts: SSHAccount[], over: Partial<SSHReport> = {}): SSHReport {
  return { machine_id: name, machine_name: name, reported_at: "2026-10-22T10:00:00Z", truncated: false, accounts, ...over };
}

test("une clé donne ses machines et ses comptes, root en tête", () => {
  const bastion = report("bastion", [account("lucas", [key(fpA, "lucas@pc")]), root([key(fpA)])]);
  const vps = report("vps", [account("deploy", [key(fpA)])]);

  const [row] = buildKeyAccess([bastion, vps]);

  expect(row?.comment).toBe("lucas@pc");
  expect(row?.privileged).toBe(true);
  expect(row?.grants.map((grant) => [grant.machineName, grant.accounts.map((a) => [a.name, a.privileged])])).toEqual([
    ["bastion", [["root", true], ["lucas", false]]],
    ["vps", [["deploy", false]]],
  ]);
});

test("la même clé dans deux fichiers d'un compte ne compte qu'une fois", () => {
  const twice = report("a", [account("lucas", [key(fpA), key(fpA, "", { file: "authorized_keys2" })])]);

  expect(buildKeyAccess([twice])[0]?.grants[0]?.accounts).toHaveLength(1);
});

test("les clés les plus exposées viennent d'abord : root, faible, nombre de machines", () => {
  const weakKey = key(fpB, "vieille", { type: "ssh-rsa", bits: 2048 });
  const a = report("a", [account("u", [key(fpA, "simple"), weakKey, key(fpC, "root")]), root([key(fpC)])]);
  const b = report("b", [account("u", [key(fpA)])]);

  expect(buildKeyAccess([a, b]).map((row) => row.fingerprint)).toEqual([fpC, fpB, fpA]);
});

test("les filtres : faibles, accès root, sur une seule machine", () => {
  const a = report("a", [root([key(fpA, "x")]), account("u", [key(fpB, "faible", { type: "ssh-dss", bits: 1024 }), key(fpC, "partagée")])]);
  const b = report("b", [account("u", [key(fpC)])]);
  const rows = buildKeyAccess([a, b]);

  expect(filterKeys(rows, "all")).toHaveLength(3);
  expect(filterKeys(rows, "weak").map((row) => row.fingerprint)).toEqual([fpB]);
  expect(filterKeys(rows, "privileged").map((row) => row.fingerprint)).toEqual([fpA]);
  expect(filterKeys(rows, "single").map((row) => row.fingerprint).sort()).toEqual([fpA, fpB].sort());
});

test("par machine : les comptes avec une clé ou illisibles, root en tête, un compte vide omis", () => {
  const machine = report("nas", [
    account("lucas", [key(fpA, "pc")]),
    account("vide", [], { state: "none" }),
    account("lu", [], { state: "read" }),
    account("secret", [], { state: "denied" }),
    root([key(fpB, "faible", { type: "ssh-rsa", bits: 1024 })]),
  ]);

  const [access] = buildMachineAccess([machine]);

  expect(access?.accounts.map((a) => a.name)).toEqual(["root", "lucas", "secret"]);
  expect(access?.accounts[0]?.keys).toEqual([{ fingerprint: fpB, comment: "faible", weak: true }]);
  expect(access?.accounts[2]?.state).toBe("denied");
});

test("les machines sont rangées par nom", () => {
  expect(buildMachineAccess([report("vps", []), report("bastion", [])]).map((m) => m.machineName)).toEqual(["bastion", "vps"]);
});

test("une machine pas relevée, tronquée ou en partie illisible n'est pas lue en entier", () => {
  expect(isComplete(report("a", [account("u", [key(fpA)])]))).toBe(true);
  expect(isComplete(report("a", [], { reported_at: null }))).toBe(false);
  expect(isComplete(report("a", [], { truncated: true }))).toBe(false);
  expect(isComplete(report("a", [account("u", [], { state: "denied" })]))).toBe(false);
  expect(isComplete(report("a", [account("u", [], { truncated: true })]))).toBe(false);
});

test("le résumé compte les clés, root, faibles, uniques et les machines mal lues", () => {
  const reports = [
    report("a", [root([key(fpA)]), account("u", [key(fpB, "", { type: "ssh-rsa", bits: 2048 }), key(fpC)])]),
    report("b", [account("u", [key(fpC)])]),
    report("c", [], { reported_at: null }),
  ];

  expect(summarizeAccess(buildKeyAccess(reports), reports)).toEqual({ keys: 3, privileged: 1, weak: 1, single: 2, incomplete: 1 });
});
