import { describe, expect, it } from "vitest";

import type { MailSettings } from "../api/settings";
import { mailFormOf, mailModeChange, mailPortFor, mailRequestOf, recipientsOf } from "./mail";

describe("mailPortFor", () => {
  it("propose 465 pour TLS dès la connexion, 587 sinon", () => {
    expect(mailPortFor("tls")).toBe(465);
    expect(mailPortFor("starttls")).toBe(587);
    expect(mailPortFor("none")).toBe(587);
  });
});

describe("recipientsOf", () => {
  it("lit virgules, points-virgules et espaces, sans doublon", () => {
    expect(recipientsOf(" a@x.fr, b@x.fr;a@x.fr  c@x.fr ")).toEqual(["a@x.fr", "b@x.fr", "c@x.fr"]);
    expect(recipientsOf("")).toEqual([]);
  });
});

describe("mailFormOf et mailRequestOf", () => {
  const relay: MailSettings = { configured: true, host: "smtp.exemple.fr", port: 587, tls: "starttls", username: "alertes", has_password: true, from: "alertes@exemple.fr" };

  it("renvoient au serveur le relais lu, sans toucher au mot de passe gardé", () => {
    expect(mailRequestOf(mailFormOf(relay))).toEqual({
      host: "smtp.exemple.fr",
      port: 587,
      tls: "starttls",
      username: "alertes",
      password: "",
      clear_password: false,
      from: "alertes@exemple.fr",
    });
  });
});

describe("mailModeChange", () => {
  const form = mailFormOf({ configured: true, host: "smtp.exemple.fr", port: 587, tls: "starttls", username: "", has_password: false, from: "" });

  it("propose le port du nouveau mode quand le port est celui de l'ancien", () => {
    expect(mailModeChange(form, "tls")).toEqual({ mode: "tls", port: "465" });
  });

  it("garde un port tapé par l'opérateur", () => {
    expect(mailModeChange({ ...form, port: "2525" }, "tls")).toEqual({ mode: "tls" });
  });
});
