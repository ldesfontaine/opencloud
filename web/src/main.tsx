import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";

import { App } from "./App";
import { I18nProvider } from "./i18n/context";
import { RefreshProvider } from "./lib/refresh";
import "./styles/base.scss";

const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <BrowserRouter>
        <I18nProvider>
          <RefreshProvider>
            <App />
          </RefreshProvider>
        </I18nProvider>
      </BrowserRouter>
    </StrictMode>,
  );
}
