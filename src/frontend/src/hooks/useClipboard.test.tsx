import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { renderToString } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

const runtimeMock = vi.hoisted(() => ({
  setText: vi.fn(),
}));

vi.mock("@wailsio/runtime", () => ({
  Clipboard: {
    SetText: runtimeMock.setText,
  },
}));

import {
  ClipboardProvider,
  type ClipboardFeedback,
  useClipboard,
  writeClipboardText,
} from "./useClipboard";

describe("writeClipboardText", () => {
  it("awaits the supported desktop host and reports success", async () => {
    const setText = vi.fn().mockResolvedValue(undefined);

    await expect(
      writeClipboardText("docker ps", { SetText: setText }),
    ).resolves.toEqual({ ok: true });
    expect(setText).toHaveBeenCalledWith("docker ps");
  });

  it("does not settle a delayed host write early", async () => {
    let finishWrite!: () => void;
    const delayedWrite = new Promise<void>((resolve) => {
      finishWrite = resolve;
    });
    let settled = false;

    const write = writeClipboardText("delayed", {
      SetText: () => delayedWrite,
    }).then((result) => {
      settled = true;
      return result;
    });

    await Promise.resolve();
    expect(settled).toBe(false);

    finishWrite();
    await expect(write).resolves.toEqual({ ok: true });
  });

  it("returns a safe failure when the host denies or rejects the write", async () => {
    const result = await writeClipboardText("secret", {
      SetText: () => Promise.reject(new Error("permission denied: secret")),
    });

    expect(result).toEqual({
      ok: false,
      code: "rejected",
      message: "Cairn could not write to the system clipboard.",
    });
    expect(result).not.toEqual(
      expect.objectContaining({ message: expect.stringContaining("secret") }),
    );
  });

  it("reports an unavailable desktop clipboard without throwing", async () => {
    await expect(writeClipboardText("value", null)).resolves.toEqual({
      ok: false,
      code: "unavailable",
      message: "The desktop clipboard is unavailable.",
    });
    await expect(writeClipboardText("value", {})).resolves.toEqual({
      ok: false,
      code: "unavailable",
      message: "The desktop clipboard is unavailable.",
    });
  });
});

describe("useClipboard", () => {
  it("fails fast when a consumer has neither a provider nor feedback handler", () => {
    expect(() => renderToString(<MissingProviderControl />)).toThrow(
      "useClipboard requires ClipboardProvider or a feedback handler",
    );
  });

  it("inherits one writer and announces success only after it resolves", async () => {
    let finishWrite!: () => void;
    runtimeMock.setText.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finishWrite = resolve;
        }),
    );
    const feedback: ClipboardFeedback[] = [];

    render(<ClipboardHarness onFeedback={(item) => feedback.push(item)} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy value" }));

    expect(feedback).toEqual([]);
    finishWrite();

    await waitFor(() =>
      expect(feedback).toEqual([
        { body: undefined, level: "ok", title: "Value copied" },
      ]),
    );
  });

  it("announces a rejected write as an error instead of success", async () => {
    runtimeMock.setText.mockRejectedValueOnce(new Error("denied"));
    const feedback: ClipboardFeedback[] = [];

    render(<ClipboardHarness onFeedback={(item) => feedback.push(item)} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy value" }));

    await waitFor(() =>
      expect(feedback).toEqual([
        {
          body: "Cairn could not write to the system clipboard.",
          level: "error",
          title: "Value not copied",
        },
      ]),
    );
  });
});

function MissingProviderControl() {
  useClipboard();
  return null;
}

function ClipboardHarness({
  onFeedback,
}: {
  onFeedback: (feedback: ClipboardFeedback) => void;
}) {
  const copyText = useClipboard(onFeedback);
  return (
    <ClipboardProvider copyText={copyText}>
      <CopyControl />
    </ClipboardProvider>
  );
}

function CopyControl() {
  const copyText = useClipboard();
  return (
    <button
      onClick={() => {
        void copyText("value", {
          failureTitle: "Value not copied",
          successTitle: "Value copied",
        });
      }}
      type="button"
    >
      Copy value
    </button>
  );
}
