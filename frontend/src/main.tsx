import React from "react";
import ReactDOM from "react-dom/client";

import { Root } from "./Root";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles/index.css";
import "./styles/cairn-loader.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("Cairn root element was not found");
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <ErrorBoundary>
      <Root />
    </ErrorBoundary>
  </React.StrictMode>,
);
