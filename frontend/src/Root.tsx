import { useState } from "react";

import App from "./App";
import CairnLoader from "./components/CairnLoader";

// App initializes behind the cinematic loader, but it must not be reachable
// by keyboard or assistive technology until the loader has released control.
export function Root() {
  const [booted, setBooted] = useState(false);

  return (
    <>
      {!booted && <CairnLoader onDone={() => setBooted(true)} />}
      <div aria-hidden={booted ? undefined : true} inert={!booted}>
        <App />
      </div>
    </>
  );
}
