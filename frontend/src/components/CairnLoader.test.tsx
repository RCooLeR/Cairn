import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAppVersion } from "../api/app";
import CairnLoader from "./CairnLoader";

vi.mock("../api/app", () => ({
  getAppVersion: vi.fn(),
}));

describe("CairnLoader", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(getAppVersion).mockResolvedValue({
      version: "test-version",
      goVersion: "test-go",
    });
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  });

  afterEach(() => {
    vi.useRealTimers();
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

    vi.advanceTimersByTime(780);
    expect(onDone).toHaveBeenCalledTimes(1);
  });
});
