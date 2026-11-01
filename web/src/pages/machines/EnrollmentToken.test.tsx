import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { EnrollmentToken } from "./EnrollmentToken";

vi.mock("../../i18n/context", () => ({ useT: () => (key: string) => key }));

const token = {
  name: "pve1",
  token: "oc_secret",
  command: "installer",
  preparation: "préparer",
  proxmox_commands: "pveum role add Role",
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  url_local: false,
  rights: [],
};

test("les commandes Proxmox se lisent en deux gestes à cocher, sans pavé d'aide", () => {
  const markup = renderToStaticMarkup(<EnrollmentToken token={token} />);
  expect(markup).toContain("machine.proxmox_token_run_title");
  expect(markup).toContain("machine.proxmox_token_secret_title");
  expect(markup).toContain("pveum role add Role");
  expect(markup).not.toContain("machine.proxmox_token_help");
});

test("sans droit Proxmox, aucun geste Proxmox", () => {
  const markup = renderToStaticMarkup(<EnrollmentToken token={{ ...token, proxmox_commands: "" }} />);
  expect(markup).not.toContain("machine.proxmox_token_run_title");
});
