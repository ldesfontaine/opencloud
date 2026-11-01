import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { Checklist } from "./Checklist";
import { CopyList } from "./Copy";

vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

test("chaque geste a sa case, cochée si elle est dans la liste", () => {
  const markup = renderToStaticMarkup(
    <Checklist
      items={[
        { id: "a", title: "Premier", content: <p>détail</p> },
        { id: "b", title: "Second", content: null },
      ]}
      done={["b"]}
      onToggle={() => undefined}
    />,
  );
  expect(markup.match(/type="checkbox"/g)).toHaveLength(2);
  expect(markup.match(/checked=""/g)).toHaveLength(1);
  expect(markup).toContain("détail");
});

test("une liste à copier montre chaque valeur avec son bouton", () => {
  const markup = renderToStaticMarkup(<CopyList values={["Lobby: Dashboard", "System: Gateways"]} />);
  expect(markup).toContain("Lobby: Dashboard");
  expect(markup.match(/<button/g)).toHaveLength(2);
});
