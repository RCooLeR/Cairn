import React, { useState } from "react";
import ReactDOM from "react-dom/client";

import App from "./App";
import CairnLoader from "./components/CairnLoader";
import { ErrorBoundary } from "./components/ErrorBoundary";
import "./styles/index.css";
import "./styles/cairn-loader.css";

// Cinematic boot loader shown over the app on first mount; App initializes
// behind it and the loader fades out when its intro completes.
function Root() {
  const [booted, setBooted] = useState(false);
  return (
    <>
      {!booted && <CairnLoader onDone={() => setBooted(true)} />}
      <App />
    </>
  );
}

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
