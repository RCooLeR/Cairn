import { useEffect, useRef, useState } from "react";

import { getAppVersion } from "../api/app";

/* ---------------------------------------------------------------------------
 * CairnLoader — cinematic boot splash for Cairn.
 *
 * Ported from the standalone cairn_loader_html (index.html / styles.css /
 * app.js): an abstract background image, a <canvas> HUD (rotating dashed
 * rings, drifting particles + links, a scan beam), and a cyan init panel
 * (per-service progress, system log, bottom strip) — all React-rendered from
 * one `progress` value. Mounted as a top-level overlay; ramps 1→100%, holds,
 * fades out, and calls onDone. Click anywhere to skip.
 * ------------------------------------------------------------------------- */

// The bar ramps to CAP, then holds there until Cairn's Go backend actually
// responds (so the loader genuinely covers startup, not a blind timer); once
// ready — and a minimum on-screen time — it finishes to 100% and fades. A hard
// max-wait dismisses anyway if the backend never answers, so it can't trap the
// user (the app then shows its own provider/setup state).
const RAMP_MS = 2600; // ease up to CAP
const MIN_MS = 1700; // minimum on-screen time before finishing
const CAP = 0.9; // ceiling held until the backend is ready
const FINISH_MS = 700; // CAP → 100% once ready
const HOLD_MS = 440; // dwell at 100% before fading
const FADE_MS = 620; // must match the .leaving transition in cairn-loader.css
const MAX_WAIT_MS = 12000; // give up waiting for the backend and dismiss anyway

const LOG_LINES = [
  "[10:24:31] Boot sequence initiated",
  "[10:24:31] Checking backend provider",
  "[10:24:32] Docker engine detected",
  "[10:24:32] Validating environment",
  "[10:24:33] Loading core modules",
  "[10:24:34] Compose service online",
  "[10:24:34] Network layer initializing",
  "[10:24:35] Security policies verified",
  "[10:24:36] Preparing UI environment",
  "[10:24:36] System ready",
];

// label, icon, and per-service fill curve (staggered) from the original app.js.
const SERVICES: { key: string; label: string; icon: string; fill: (p: number) => number }[] = [
  { key: "core", label: "Core Services", icon: "▣", fill: (p) => Math.min(100, p * 155) },
  { key: "engine", label: "Docker Engine", icon: "⬡", fill: (p) => Math.min(100, Math.max(0, (p - 0.1) * 130)) },
  { key: "compose", label: "Compose Module", icon: "◇", fill: (p) => Math.min(100, Math.max(0, (p - 0.24) * 118)) },
  { key: "network", label: "Network Layer", icon: "◌", fill: (p) => Math.min(100, Math.max(0, (p - 0.42) * 110)) },
  { key: "agent", label: "Local Agent", icon: "⌂", fill: (p) => Math.min(100, Math.max(0, (p - 0.58) * 110)) },
];

const easeOutCubic = (t: number) => 1 - Math.pow(1 - Math.min(1, Math.max(0, t)), 3);

function stateFor(p: number): string {
  if (p < 0.15) return "Booting";
  if (p < 0.36) return "Initializing services...";
  if (p < 0.62) return "Checking backend...";
  if (p < 0.86) return "Synchronizing modules...";
  if (p < 1) return "Finalizing...";
  return "Ready";
}

type RGB = [number, number, number];
type Particle = { x: number; y: number; vx: number; vy: number; r: number; color: RGB; phase: number };
type Ring = { radius: number; speed: number; offset: number };

export default function CairnLoader({ onDone }: { onDone: () => void }) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const [prog, setProg] = useState(0.01);
  const [leaving, setLeaving] = useState(false);
  const leavingRef = useRef(false);
  const doneRef = useRef(false);
  const readyRef = useRef(false); // backend responded (or we gave up waiting)

  const finish = () => {
    if (doneRef.current) return;
    doneRef.current = true;
    onDone();
  };

  const beginLeave = () => {
    if (leavingRef.current) return;
    leavingRef.current = true;
    setLeaving(true);
    window.setTimeout(finish, FADE_MS + 160); // fallback if transitionend doesn't fire
  };

  // ---- readiness: hold the bar at CAP until Cairn's Go backend answers ----
  useEffect(() => {
    let alive = true;
    const t0 = performance.now();
    void (async () => {
      while (alive) {
        try {
          await getAppVersion(); // resolves once the backend is reachable
          break;
        } catch {
          if (performance.now() - t0 > MAX_WAIT_MS) break;
          await new Promise((r) => setTimeout(r, 350));
        }
      }
      if (alive) readyRef.current = true;
    })();
    // Hard backstop so even a hung call can't keep the loader up forever.
    const hard = window.setTimeout(() => {
      readyRef.current = true;
    }, MAX_WAIT_MS);
    return () => {
      alive = false;
      window.clearTimeout(hard);
    };
  }, []);

  // ---- canvas FX (rings + particles + links + scan beam), ported from app.js
  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;

    const CYAN: RGB = [22, 216, 207];
    const CYAN2: RGB = [23, 245, 230];
    const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
    const mix = (a: RGB, b: RGB, t: number): RGB => [
      Math.round(lerp(a[0], b[0], t)),
      Math.round(lerp(a[1], b[1], t)),
      Math.round(lerp(a[2], b[2], t)),
    ];
    const rgba = (c: RGB, a: number) => `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${a})`;

    let w = 0;
    let h = 0;
    let particles: Particle[] = [];
    let rings: Ring[] = [];
    let raf = 0;
    const start = performance.now();
    let completed = false;
    let finishStart = 0; // timestamp when CAP → 100% began
    let finishFrom = 0; // progress at the moment the finish began

    const buildFx = () => {
      const count = Math.min(110, Math.floor((w * h) / 19000));
      particles = Array.from({ length: count }, () => ({
        x: Math.random() * w,
        y: Math.random() * h,
        vx: (Math.random() - 0.5) * 0.13,
        vy: (Math.random() - 0.5) * 0.13,
        r: 0.8 + Math.random() * 1.9,
        color: Math.random() > 0.25 ? CYAN : CYAN2,
        phase: Math.random() * Math.PI * 2,
      }));
      const scale = Math.min(w, h);
      rings = Array.from({ length: 9 }, (_, i) => ({
        radius: scale * (0.12 + i * 0.026),
        speed: 0.00012 + i * 0.000025,
        offset: Math.random() * Math.PI * 2,
      }));
    };

    const resize = () => {
      const dpr = Math.max(1, window.devicePixelRatio || 1);
      w = window.innerWidth;
      h = window.innerHeight;
      canvas.width = Math.floor(w * dpr);
      canvas.height = Math.floor(h * dpr);
      canvas.style.width = w + "px";
      canvas.style.height = h + "px";
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      buildFx();
    };

    const draw = (now: number) => {
      // Ramp to CAP, hold there until the backend is ready, then ease to 100%.
      let progress: number;
      if (finishStart === 0) {
        progress = Math.min(CAP, easeOutCubic((now - start) / RAMP_MS));
        if (readyRef.current && now - start >= MIN_MS) {
          finishStart = now;
          finishFrom = progress;
        }
      } else {
        const t = Math.min(1, (now - finishStart) / FINISH_MS);
        progress = finishFrom + (1 - finishFrom) * easeOutCubic(t);
        if (t >= 1 && !completed) {
          completed = true;
          window.setTimeout(beginLeave, HOLD_MS);
        }
      }
      setProg(progress);

      ctx.clearRect(0, 0, w, h);
      ctx.save();
      ctx.globalCompositeOperation = "screen";
      const cx = w * 0.43;
      const cy = h * 0.5;
      const scale = Math.min(w, h);

      rings.forEach((ring, i) => {
        const a = ring.offset + now * ring.speed;
        ctx.strokeStyle = rgba(CYAN, 0.035 + i * 0.005);
        ctx.lineWidth = i % 3 === 0 ? 1.5 : 1;
        ctx.setLineDash(i % 2 === 0 ? [12, 16] : [3, 10]);
        ctx.lineDashOffset = -now * (0.018 + i * 0.004);
        ctx.beginPath();
        ctx.arc(cx, cy, ring.radius, a, a + Math.PI * (1.1 + (i % 4) * 0.18));
        ctx.stroke();
      });
      ctx.setLineDash([]);

      const beamY = cy - scale * 0.22 + ((now * 0.055) % (scale * 0.44));
      const beam = ctx.createLinearGradient(cx - scale * 0.34, beamY, cx + scale * 0.34, beamY);
      beam.addColorStop(0, "rgba(22,216,207,0)");
      beam.addColorStop(0.35, "rgba(22,216,207,0.16)");
      beam.addColorStop(0.5, "rgba(255,255,255,0.42)");
      beam.addColorStop(0.65, "rgba(23,245,230,0.16)");
      beam.addColorStop(1, "rgba(23,245,230,0)");
      ctx.strokeStyle = beam;
      ctx.lineWidth = 1.6;
      ctx.beginPath();
      ctx.moveTo(cx - scale * 0.36, beamY);
      ctx.lineTo(cx + scale * 0.36, beamY);
      ctx.stroke();

      for (const p of particles) {
        p.x += p.vx;
        p.y += p.vy;
        if (p.x < -20) p.x = w + 20;
        if (p.x > w + 20) p.x = -20;
        if (p.y < -20) p.y = h + 20;
        if (p.y > h + 20) p.y = -20;
        const a = 0.12 + (Math.sin(now * 0.001 + p.phase) + 1) * 0.13;
        ctx.fillStyle = rgba(p.color, a);
        ctx.beginPath();
        ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
        ctx.fill();
      }
      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
          const a = particles[i];
          const b = particles[j];
          const dx = a.x - b.x;
          const dy = a.y - b.y;
          const d2 = dx * dx + dy * dy;
          if (d2 < 11500) {
            ctx.strokeStyle = rgba(mix(a.color, b.color, 0.5), 0.045 * (1 - d2 / 11500));
            ctx.lineWidth = 1;
            ctx.beginPath();
            ctx.moveTo(a.x, a.y);
            ctx.lineTo(b.x, b.y);
            ctx.stroke();
          }
        }
      }
      ctx.restore();

      if (!leavingRef.current) raf = requestAnimationFrame(draw);
    };

    resize();
    window.addEventListener("resize", resize);
    raf = requestAnimationFrame(draw);
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener("resize", resize);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- HUD values from progress ----
  const pct = Math.max(1, Math.round(prog * 100));
  const logCount = Math.max(3, Math.min(LOG_LINES.length, Math.floor(prog * LOG_LINES.length) + 1));

  return (
    <div
      className={`cairn-loader${leaving ? " leaving" : ""}`}
      role="progressbar"
      aria-label="Initializing Cairn"
      aria-valuenow={pct}
      aria-valuemin={0}
      aria-valuemax={100}
      title="Click to skip"
      onClick={beginLeave}
      onTransitionEnd={(e) => {
        if (e.propertyName === "opacity" && leavingRef.current) finish();
      }}
    >
      <div className="bg" />
      <canvas className="fx" ref={canvasRef} />
      <div className="scan" />

      <section className="hud" aria-label="Cairn initialization screen">
        <div className="corner tl" />
        <div className="corner tr" />
        <div className="corner bl" />
        <div className="corner br" />

        <header className="brand">
          <img src="/cairn-icon.png" alt="" />
          <div>
            <div className="brand-name">Cairn</div>
            <div className="brand-sub">Docker · Compose · Under control</div>
          </div>
        </header>

        <section className="hero">
          <h1>Cairn</h1>
          <p>Building the foundation. Powering your containers.</p>
          <div className="hero-line">
            <span style={{ width: `${pct}%` }} />
          </div>
        </section>

        <aside className="status-panel">
          <div className="panel-header">
            <span>Cairn Init System</span>
            <span>WSL2</span>
          </div>

          <div className="main-percent">
            <span>{pct}%</span>
            <small>{stateFor(prog)}</small>
          </div>

          <div className="primary-bar">
            <div style={{ width: `${pct}%` }} />
          </div>

          <div className="services">
            {SERVICES.map((svc) => {
              const v = Math.round(svc.fill(prog));
              const state = v >= 100 ? "OK" : v > 0 ? "RUNNING" : "WAITING";
              return (
                <div className="service" key={svc.key}>
                  <div className="service-icon">{svc.icon}</div>
                  <div className="service-main">
                    <div className="service-row">
                      <span>{svc.label}</span>
                      <strong>{v}%</strong>
                    </div>
                    <div className="service-bar">
                      <span style={{ width: `${v}%` }} />
                    </div>
                  </div>
                  <em>{state}</em>
                </div>
              );
            })}
          </div>

          <div className="log-title">System Log</div>
          <div className="log">
            {LOG_LINES.slice(0, logCount).map((line) => (
              <div key={line}>{line}</div>
            ))}
          </div>
        </aside>

        <footer className="bottom-strip">
          <span>System Initialization</span>
          <div className="strip-bar">
            <span style={{ width: `${pct}%` }} />
          </div>
          <strong>{pct}%</strong>
        </footer>
      </section>
    </div>
  );
}
