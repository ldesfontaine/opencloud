import { describe, expect, it } from "vitest";

import { mailPortFor, recipientsOf } from "./mail";

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
