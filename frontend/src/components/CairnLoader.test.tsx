import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAppVersion } from "../api/app";
import CairnLoader from "./CairnLoader";

vi.mock("../api/app", () => ({
  getAppVersion: vi.fn(),
}));

type MotionPreferenceController = {
  mediaQuery: MediaQueryList;
  setMatches: (matches: boolean) => void;
};

let animationFrameID = 0;
let cancelAnimationFrameMock: ReturnType<typeof vi.fn>;
let canvasContext: CanvasRenderingContext2D;
let motionPreference: MotionPreferenceController;
let requestAnimationFrameMock: ReturnType<typeof vi.fn>;

describe("CairnLoader", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(getAppVersion).mockResolvedValue({
      version: "test-version",
      goVersion: "test-go",
    });
    motionPreference = createMotionPreferenceController();
    vi.stubGlobal(
      "matchMedia",
      vi.fn(() => motionPreference.mediaQuery),
    );
    animationFrameID = 0;
    requestAnimationFrameMock = vi.fn(() => ++animationFrameID);
    cancelAnimationFrameMock = vi.fn();
    vi.stubGlobal("requestAnimationFrame", requestAnimationFrameMock);
    vi.stubGlobal("cancelAnimationFrame", cancelAnimationFrameMock);
    canvasContext = createCanvasContext();
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
      canvasContext,
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("exposes progress without swallowing the keyboard-accessible skip control", () => {
    const onDone = vi.fn();
    render(<CairnLoader onDone={onDone} />);

    const progress = screen.getByRole("progressbar", {
      name: "Initializing Cairn",
    });
    const skip = screen.getByRole("button", { name: "Skip intro" });

    expect(progress).not.toContainElement(skip);
    expect(skip).toBeEnabled();

    skip.focus();
    expect(skip).toHaveFocus();
    fireEvent.click(skip);

    expect(skip).toBeDisabled();
    expect(onDone).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(780));
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("renders one static canvas frame without RAF and still completes in reduced motion", async () => {
    motionPreference.setMatches(true);
    const onDone = vi.fn();

    render(<CairnLoader onDone={onDone} />);

    expect(requestAnimationFrameMock).not.toHaveBeenCalled();
    expect(canvasContext.clearRect).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(
      screen.getByRole("progressbar", { name: "Initializing Cairn" }),
    ).toHaveAttribute("aria-valuenow", "100");
    expect(onDone).toHaveBeenCalledTimes(1);
    expect(requestAnimationFrameMock).not.toHaveBeenCalled();
  });

  it("stops and restarts canvas RAF when the motion preference changes", () => {
    const view = render(<CairnLoader onDone={vi.fn()} />);

    expect(requestAnimationFrameMock).toHaveBeenCalledTimes(1);
    expect(cancelAnimationFrameMock).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(600));
    const progressBeforeChange = Number(
      screen
        .getByRole("progressbar", { name: "Initializing Cairn" })
        .getAttribute("aria-valuenow"),
    );

    act(() => motionPreference.setMatches(true));

    expect(cancelAnimationFrameMock).toHaveBeenCalledWith(1);
    expect(requestAnimationFrameMock).toHaveBeenCalledTimes(1);
    expect(canvasContext.clearRect).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(100));
    expect(
      Number(
        screen
          .getByRole("progressbar", { name: "Initializing Cairn" })
          .getAttribute("aria-valuenow"),
      ),
    ).toBeGreaterThan(progressBeforeChange);

    act(() => motionPreference.setMatches(false));

    expect(requestAnimationFrameMock).toHaveBeenCalledTimes(2);
    view.unmount();
    expect(cancelAnimationFrameMock).toHaveBeenCalledWith(2);
    expect(
      motionPreference.mediaQuery.removeEventListener,
    ).toHaveBeenCalledWith("change", expect.any(Function));
  });
});

function createMotionPreferenceController(): MotionPreferenceController {
  let matches = false;
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  const mediaQuery = {
    addEventListener: vi.fn(
      (type: string, listener: (event: MediaQueryListEvent) => void) => {
        if (type === "change") listeners.add(listener);
      },
    ),
    addListener: vi.fn(),
    dispatchEvent: vi.fn(() => true),
    get matches() {
      return matches;
    },
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    removeEventListener: vi.fn(
      (type: string, listener: (event: MediaQueryListEvent) => void) => {
        if (type === "change") listeners.delete(listener);
      },
    ),
    removeListener: vi.fn(),
  } as unknown as MediaQueryList;

  return {
    mediaQuery,
    setMatches(nextMatches) {
      matches = nextMatches;
      const event = {
        matches,
        media: mediaQuery.media,
      } as MediaQueryListEvent;
      for (const listener of listeners) listener(event);
    },
  };
}

function createCanvasContext(): CanvasRenderingContext2D {
  return {
    arc: vi.fn(),
    beginPath: vi.fn(),
    clearRect: vi.fn(),
    createLinearGradient: vi.fn(() => ({ addColorStop: vi.fn() })),
    fill: vi.fn(),
    lineTo: vi.fn(),
    moveTo: vi.fn(),
    restore: vi.fn(),
    save: vi.fn(),
    setLineDash: vi.fn(),
    setTransform: vi.fn(),
    stroke: vi.fn(),
  } as unknown as CanvasRenderingContext2D;
}
