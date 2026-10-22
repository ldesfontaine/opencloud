import { expect, test } from "vitest";

import type { AuthState } from "../api/auth";
import { accessLabel, isForwardInsecure, needsFirstSetup, passwordFormError } from "./auth";

const unconfigured: AuthState = { mode: "none", username: "", enabled_at: null, password_min_length: 12, allowed_users: null };
const basic: AuthState = { mode: "basic", username: "lucas", enabled_at: "2026-10-10T08:00:00Z", password_min_length: 12, allowed_users: null };
const forward: AuthState = {
  mode: "forward",
  username: "marie",
  header: "Remote-User",
  enabled_at: "2026-10-11T08:00:00Z",
  password_min_length: 12,
  allowed_users: ["marie"],
};

test("accessLabel dit l'identifiant quand l'accès est protégé", () => {
  expect(accessLabel(basic)).toEqual({ key: "auth.side_protected", username: "lucas" });
});

test("accessLabel dit le mandataire et l'identité qu'il transmet", () => {
  expect(accessLabel(forward)).toEqual({ key: "auth.side_protected_forward", username: "marie" });
});

test("accessLabel ne dit rien sans compte, ni avant la réponse", () => {
  expect(accessLabel(unconfigured)).toBeNull();
  expect(accessLabel(null)).toBeNull();
});

test("needsFirstSetup n'ouvre la première configuration que sur la réponse du serveur", () => {
  expect(needsFirstSetup(unconfigured)).toBe(true);
  expect(needsFirstSetup(basic)).toBe(false);
  expect(needsFirstSetup(forward)).toBe(false);
  expect(needsFirstSetup(null)).toBe(false);
});

test("passwordFormError refuse un mot de passe trop court, compté en caractères", () => {
  expect(passwordFormError("onze carac.", "onze carac.", 12)).toBe("auth.password_too_short");
  expect(passwordFormError("éééééééééééé", "éééééééééééé", 12)).toBeNull();
});

test("passwordFormError refuse une confirmation différente", () => {
  expect(passwordFormError("correct horse battery", "correct horse batterie", 12)).toBe("auth.password_mismatch");
  expect(passwordFormError("correct horse battery", "correct horse battery", 12)).toBeNull();
});

test("isForwardInsecure ne parle que du mode mandataire que le serveur dit sans preuve", () => {
  expect(isForwardInsecure({ ...forward, forward_insecure: true })).toBe(true);
  expect(isForwardInsecure(forward)).toBe(false);
  expect(isForwardInsecure({ ...basic, forward_insecure: true })).toBe(false);
  expect(isForwardInsecure(unconfigured)).toBe(false);
  expect(isForwardInsecure(null)).toBe(false);
});
