import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { EnrollmentToken } from "./EnrollmentToken";

vi.mock("../../i18n/context", () => ({ useT: () => (key: string) => key }));

const token = {
  name: "pve1",
  token: "oc_secret",
  steps: [
    { id: "prepare" as const, command: "préparer" },
    { id: "enroll" as const, command: "installer" },
    { id: "unit" as const, command: "cat > unité" },
  ],
  proxmox_commands: "pveum role add Role",
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  url_local: false,
  url_suggestion: "",
  rights: [],
};

const checklist = { done: [], toggle: () => undefined };

test("les commandes Proxmox se lisent en deux gestes à cocher", () => {
  const markup = renderToStaticMarkup(<EnrollmentToken token={token} checklist={checklist} />);
  expect(markup).toContain("machine.proxmox_token_run_title");
  expect(markup).toContain("machine.proxmox_token_secret_title");
  expect(markup).toContain("pveum role add Role");
});

test("sans droit Proxmox, aucun geste Proxmox", () => {
  const markup = renderToStaticMarkup(<EnrollmentToken token={{ ...token, proxmox_commands: "" }} checklist={checklist} />);
  expect(markup).not.toContain("machine.proxmox_token_run_title");
});

test("chaque étape est un titre, une ligne et un seul bloc ; le jeton n'est affiché nulle part ailleurs", () => {
  const markup = renderToStaticMarkup(<EnrollmentToken token={token} checklist={checklist} />);
  for (const id of ["prepare", "enroll", "unit"]) {
    expect(markup).toContain(`machine.install_${id}_title`);
    expect(markup).toContain(`machine.install_${id}_text`);
  }
  expect(markup).not.toContain("machine.install_binary_title");
  expect(markup).not.toContain("oc_secret");
  expect(markup.indexOf("préparer")).toBeLessThan(markup.indexOf("installer"));
  expect(markup.indexOf("installer")).toBeLessThan(markup.indexOf("cat &gt; unité"));
});
