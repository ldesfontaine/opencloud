import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import { ApiError } from "../api/client";
import { Failure } from "./Failure";

vi.mock("../i18n/context", () => ({
  useT: () => (key: string, ...args: (string | number)[]) => (args.length === 0 ? key : `${key}(${args.join("|")})`),
}));

function render(error: ApiError | string | null, gesture?: Parameters<typeof Failure>[0]["gesture"]): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <Failure error={error} gesture={gesture} />
    </MemoryRouter>,
  );
}

test("une erreur ordinaire se dit en note, sans geste", () => {
  const markup = render(new ApiError(422, "probe.target_invalid"));
  expect(markup).toContain("probe.target_invalid");
  expect(markup).not.toContain("<a ");
});

test("un refus de droit porte son geste dès que l'écran dit la machine", () => {
  const refused = new ApiError(409, "backup.right_missing");
  const markup = render(refused, { machineID: "m7", path: "/srv/backups" });
  expect(markup).toContain("backup.right_missing");
  expect(markup).toContain('href="/machines/m7/reenroller?droit=backup.receive&amp;chemin=%2Fsrv%2Fbackups"');
  expect(markup).toContain("machine.reenroll_with(machine.capability_backup_receive)");
  // Sans machine, le refus se dit, sans lien qui mènerait nulle part.
  expect(render(refused)).not.toContain("<a ");
});

test("le droit que le refus ne nomme pas vient de l'écran", () => {
  const refused = new ApiError(409, "action.right_missing");
  expect(render(refused, { machineID: "m1" })).not.toContain("<a ");
  expect(render(refused, { machineID: "m1", right: "files.write" })).toContain('href="/machines/m1/reenroller?droit=files.write"');
});

test("un refus par ancre locale montre la commande à lancer en root sur la machine", () => {
  const refused = new ApiError(409, "anchor.backup_guest_missing", { error: "anchor.backup_guest_missing", command: "/var/lib/opencloud-agent/bin/current agent trust add backup.guest 5012" });
  const markup = render(refused);
  expect(markup).toContain("anchor.backup_guest_missing");
  expect(markup).toContain("anchor.run_command");
  expect(markup).toContain("/var/lib/opencloud-agent/bin/current agent trust add backup.guest 5012");
});
