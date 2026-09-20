import type { ReactNode } from "react";
import { createContext, useCallback, useContext } from "react";

import { Clipboard } from "@wailsio/runtime";

export type ClipboardWriteHost = {
  SetText?: (text: string) => Promise<void>;
};

export type ClipboardWriteResult =
  | { ok: true }
  | {
      ok: false;
      code: "unavailable" | "rejected";
      message: string;
    };

export type ClipboardFeedback = {
  body?: string;
  level: "ok" | "error";
  title: string;
};

export type ClipboardCopyOptions = {
  announce?: boolean;
  failureTitle?: string;
  successBody?: string;
  successTitle?: string;
};

export type CopyText = (
  text: string,
  options?: ClipboardCopyOptions,
) => Promise<ClipboardWriteResult>;

type ClipboardFeedbackHandler = (feedback: ClipboardFeedback) => void;

const desktopClipboard: ClipboardWriteHost = Clipboard;
const ClipboardContext = createContext<CopyText | null>(null);

export async function writeClipboardText(
  text: string,
  host: ClipboardWriteHost | null = desktopClipboard,
): Promise<ClipboardWriteResult> {
  if (!host || typeof host.SetText !== "function") {
    return {
      ok: false,
      code: "unavailable",
      message: "The desktop clipboard is unavailable.",
    };
  }

  try {
    await host.SetText(text);
    return { ok: true };
  } catch {
    return {
      ok: false,
      code: "rejected",
      message: "Cairn could not write to the system clipboard.",
    };
  }
}

export function ClipboardProvider({
  children,
  copyText,
}: {
  children: ReactNode;
  copyText: CopyText;
}) {
  return <ClipboardContext value={copyText}>{children}</ClipboardContext>;
}

export function useClipboard(onFeedback?: ClipboardFeedbackHandler): CopyText {
  const inheritedCopyText = useContext(ClipboardContext);
  const localCopyText = useCallback<CopyText>(
    async (text, options = {}) => {
      const result = await writeClipboardText(text);
      if (options.announce !== false && onFeedback) {
        onFeedback(
          result.ok
            ? {
                body: options.successBody,
                level: "ok",
                title: options.successTitle ?? "Copied",
              }
            : {
                body: result.message,
                level: "error",
                title: options.failureTitle ?? "Copy failed",
              },
        );
      }
      return result;
    },
    [onFeedback],
  );

  if (onFeedback) {
    return localCopyText;
  }
  if (!inheritedCopyText) {
    throw new Error(
      "useClipboard requires ClipboardProvider or a feedback handler",
    );
  }
  return inheritedCopyText;
}
