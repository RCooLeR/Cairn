import "@testing-library/jest-dom/vitest";

import { act, cleanup } from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, vi } from "vitest";

import { type ConsoleMessageMatcher, TestConsolePolicy } from "./consolePolicy";

// ResponsiveContainer intentionally starts at -1 x -1 until its first browser
// measurement. jsdom has no layout pass, so give test renders a valid first
// frame; TestResizeObserver below still replaces it with the container's actual
// explicit dimensions.
vi.mock("recharts", async (importOriginal) => {
  const actual = await importOriginal<typeof import("recharts")>();
  const { createElement, forwardRef } = await import("react");
  const TestResponsiveContainer = forwardRef<
    HTMLDivElement,
    ComponentProps<typeof actual.ResponsiveContainer>
  >((props, ref) =>
    createElement(actual.ResponsiveContainer, {
      initialDimension: { height: 400, width: 800 },
      ...props,
      ref,
    }),
  );
  TestResponsiveContainer.displayName = "TestResponsiveContainer";

  return { ...actual, ResponsiveContainer: TestResponsiveContainer };
});

const consolePolicy = new TestConsolePolicy();

// Wails emits this browser-mode notice while its runtime module is imported in
// jsdom. It does not indicate an application/test defect, and matching both its
// title and official documentation link keeps the exception intentionally narrow.
consolePolicy.allowDiagnostic(
  "warn",
  "Wails browser-environment notice",
  (arguments_) =>
    arguments_.length === 4 &&
    typeof arguments_[0] === "string" &&
    arguments_[0].includes("⚠️ Browser Environment Detected") &&
    arguments_[0].includes(
      "https://v3.wails.io/learn/build/#using-a-browser-for-development",
    ) &&
    arguments_[1] ===
      "background: #ffffff; color: #000000; font-weight: bold; padding: 4px 8px; border-radius: 4px; border: 2px solid #000000;" &&
    arguments_[2] === "background: transparent;" &&
    arguments_[3] === "color: #ffffff; font-style: italic; font-weight: bold;",
);

for (const level of ["error", "warn"] as const) {
  Object.defineProperty(console, level, {
    configurable: true,
    value: (...arguments_: unknown[]) =>
      consolePolicy.record(level, arguments_),
    writable: true,
  });
}

export function allowConsoleErrorOnce(
  description: string,
  matcher: ConsoleMessageMatcher,
) {
  consolePolicy.allowOnce("error", description, matcher);
}

export function allowConsoleWarningOnce(
  description: string,
  matcher: ConsoleMessageMatcher,
) {
  consolePolicy.allowOnce("warn", description, matcher);
}

afterEach(async () => {
  let teardownError: unknown;
  try {
    await act(async () => {
      cleanup();
      // Effect cleanup commonly attaches rejection handlers to host promises.
      // Drain that microtask turn before attributing diagnostics to this test.
      await Promise.resolve();
    });
    window.localStorage.clear();
    window.sessionStorage.clear();
  } catch (error: unknown) {
    teardownError = error;
  }

  try {
    consolePolicy.verifyAndReset();
  } catch (policyError: unknown) {
    if (teardownError !== undefined) {
      throw Object.assign(
        new Error(
          [
            "Test teardown and console verification both failed:",
            describeFailure(teardownError),
            describeFailure(policyError),
          ].join("\n"),
        ),
        { causes: [teardownError, policyError] },
      );
    }
    throw policyError;
  }
  if (teardownError !== undefined) {
    throw teardownError;
  }
});

function describeFailure(error: unknown) {
  return error instanceof Error
    ? `${error.name}: ${error.message}`
    : String(error);
}

// jsdom deliberately omits the optional native canvas implementation. A null
// context is the browser-supported fallback used by CairnLoader; focused canvas
// tests replace this method with a complete deterministic context.
Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
  configurable: true,
  value: () => null,
  writable: true,
});

if (typeof window.matchMedia !== "function") {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: (query: string): MediaQueryList => ({
      addEventListener: () => undefined,
      addListener: () => undefined,
      dispatchEvent: () => false,
      matches: false,
      media: query,
      onchange: null,
      removeEventListener: () => undefined,
      removeListener: () => undefined,
    }),
    writable: true,
  });
}

if (typeof globalThis.ResizeObserver === "undefined") {
  const observedDimension = (
    target: Element,
    dimension: "height" | "width",
    fallback: number,
  ) => {
    const bounds = target.getBoundingClientRect();
    if (bounds[dimension] > 0) return bounds[dimension];

    for (
      let element: Element | null = target;
      element;
      element = element.parentElement
    ) {
      if (!(element instanceof HTMLElement)) continue;
      const value = Number.parseFloat(element.style[dimension]);
      if (element.style[dimension].endsWith("px") && value > 0) return value;
    }

    return fallback;
  };

  const observedRect = (target: Element): DOMRectReadOnly => {
    const width = observedDimension(target, "width", 800);
    const height = observedDimension(target, "height", 400);
    return {
      bottom: height,
      height,
      left: 0,
      right: width,
      top: 0,
      width,
      x: 0,
      y: 0,
      toJSON: () => ({ height, width, x: 0, y: 0 }),
    };
  };

  class TestResizeObserver implements ResizeObserver {
    constructor(private readonly callback: ResizeObserverCallback) {}

    disconnect() {
      return undefined;
    }

    observe(target: Element) {
      const contentRect = observedRect(target);
      this.callback(
        [
          {
            borderBoxSize: [],
            contentBoxSize: [],
            contentRect,
            devicePixelContentBoxSize: [],
            target,
          },
        ],
        this,
      );
    }

    unobserve() {
      return undefined;
    }
  }

  Object.defineProperty(globalThis, "ResizeObserver", {
    configurable: true,
    value: TestResizeObserver,
    writable: true,
  });
}
