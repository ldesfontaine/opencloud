import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";

import { App } from "./App";
import { I18nProvider } from "./i18n/context";
import { RefreshProvider } from "./api/refresh";
import { FirstSetupGate } from "./pages/FirstSetup";
import "./styles/base.scss";

const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <BrowserRouter>
        <I18nProvider>
          <RefreshProvider>
            {/* Sans compte, la première configuration remplace l'interface. */}
            <FirstSetupGate>
              <App />
            </FirstSetupGate>
          </RefreshProvider>
        </I18nProvider>
      </BrowserRouter>
    </StrictMode>,
  );
}
