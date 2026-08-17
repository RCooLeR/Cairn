import { useLayoutEffect, useRef, useState } from "react";

import App from "./App";
import CairnLoader from "./components/CairnLoader";

// App initializes behind the cinematic loader, but it must not be reachable
// by keyboard or assistive technology until the loader has released control.
export function Root() {
  const [booted, setBooted] = useState(false);
  const appGateRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const gate = appGateRef.current;
    if (!gate) {
      return;
    }
    if (booted) {
      gate.removeAttribute("inert");
    } else {
      gate.setAttribute("inert", "");
    }
  }, [booted]);

  return (
    <>
      {!booted && <CairnLoader onDone={() => setBooted(true)} />}
      <div aria-hidden={booted ? undefined : true} ref={appGateRef}>
        <App />
      </div>
    </>
  );
}
