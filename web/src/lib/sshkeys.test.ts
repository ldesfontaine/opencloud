import { expect, test } from "vitest";

import type { SSHAccount, SSHKey, SSHReport } from "../api/sshkeys";
import { hasDenied, isWeak, shortFingerprint } from "./sshkeys";

const fpA = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8";

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

test("hasDenied ne regarde que les comptes refusés", () => {
  expect(hasDenied(report("a", [account("root", []), account("x", [], { state: "denied" })]))).toBe(true);
  expect(hasDenied(report("a", [account("root", [], { state: "unsafe" })]))).toBe(false);
});
