import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
  window.localStorage.clear();
  window.sessionStorage.clear();
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
