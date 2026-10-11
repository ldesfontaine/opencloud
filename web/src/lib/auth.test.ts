import { expect, test } from "vitest";

import type { AuthState } from "../api/types";
import { accessLabel, isInterfaceOpen, passwordFormError } from "./auth";

const open: AuthState = { mode: "none", username: "", enabled_at: null, password_min_length: 12 };
const basic: AuthState = { mode: "basic", username: "lucas", enabled_at: "2026-10-10T08:00:00Z", password_min_length: 12 };
const forward: AuthState = {
  mode: "forward",
  username: "marie",
  header: "Remote-User",
  enabled_at: "2026-10-11T08:00:00Z",
  password_min_length: 12,
};

test("accessLabel dit l'identifiant quand l'accès est protégé", () => {
  expect(accessLabel(basic)).toEqual({ key: "auth.side_protected", username: "lucas" });
});

test("accessLabel dit le mandataire et l'identité qu'il transmet", () => {
  expect(accessLabel(forward)).toEqual({ key: "auth.side_protected_forward", username: "marie" });
  expect(isInterfaceOpen(forward)).toBe(false);
});

test("accessLabel dit l'accès ouvert sans identifiant, et rien avant la réponse", () => {
  expect(accessLabel(open)).toEqual({ key: "auth.side_open", username: null });
  expect(accessLabel(null)).toBeNull();
});

test("isInterfaceOpen n'affirme l'ouverture que sur la réponse du serveur", () => {
  expect(isInterfaceOpen(open)).toBe(true);
  expect(isInterfaceOpen(basic)).toBe(false);
  expect(isInterfaceOpen(null)).toBe(false);
});

test("passwordFormError refuse un mot de passe trop court, compté en caractères", () => {
  expect(passwordFormError("onze carac.", "onze carac.", 12)).toBe("auth.password_too_short");
  expect(passwordFormError("éééééééééééé", "éééééééééééé", 12)).toBeNull();
});

test("passwordFormError refuse une confirmation différente", () => {
  expect(passwordFormError("correct horse battery", "correct horse batterie", 12)).toBe("auth.password_mismatch");
  expect(passwordFormError("correct horse battery", "correct horse battery", 12)).toBeNull();
});
