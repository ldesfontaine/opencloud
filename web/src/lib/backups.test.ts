import { describe, expect, it } from "vitest";

import type { Backups } from "../api/backups";
import { isBackedUp, passphraseProblem } from "./backups";

const keyring = { recipient: "age1x", fingerprint: "0123456789abcdef", created_at: "2026-09-12T12:00:00Z" };
const destination = { id: "bkd_1", name: "store-1", machine_id: "m1", machine_name: "store-1", path: "/srv/backups", right: "ok" as const, created_at: "2026-09-12T12:00:00Z" };
const plan = { destination_id: "bkd_1", keep: 14, schedule: "daily", last_attempt_at: null, last_error: "", last_run: null };

describe("passphraseProblem", () => {
  it("refuses two phrases that differ, before saying anything of their length", () => {
    expect(passphraseProblem("une phrase de test assez longue", "une phrase de test assez longuE")).toBe("backup.passphrase_mismatch");
    expect(passphraseProblem("court", "autre")).toBe("backup.passphrase_mismatch");
  });

  it("refuses a short phrase, counted in characters rather than bytes", () => {
    expect(passphraseProblem("court", "court")).toBe("backup.passphrase_too_short");
    // 15 caractères, 19 octets : trop court.
    expect(passphraseProblem("é".repeat(15), "é".repeat(15))).toBe("backup.passphrase_too_short");
    expect(passphraseProblem("é".repeat(16), "é".repeat(16))).toBeNull();
  });

  it("does not count the spaces at the ends, as the server does", () => {
    expect(passphraseProblem("  quinze caractèr  ", "  quinze caractèr  ")).toBe("backup.passphrase_too_short");
  });
});

describe("isBackedUp", () => {
  it("needs a key, a plan and the destination of that plan", () => {
    expect(isBackedUp({ keyring, destinations: [destination], plan })).toBe(true);
    expect(isBackedUp({ keyring: null, destinations: [destination], plan })).toBe(false);
    expect(isBackedUp({ keyring, destinations: [destination], plan: null })).toBe(false);
    expect(isBackedUp({ keyring, destinations: [], plan })).toBe(false);
  });

  it("is not backed up by a plan that points elsewhere", () => {
    const elsewhere: Backups = { keyring, destinations: [destination], plan: { ...plan, destination_id: "bkd_2" } };
    expect(isBackedUp(elsewhere)).toBe(false);
  });
});
