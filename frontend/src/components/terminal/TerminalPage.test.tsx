import { describe, expect, it } from "vitest";

import { decodeBase64Bytes, encodeTerminalInput } from "./terminalEncoding";

describe("terminal encoding helpers", () => {
  it("round-trips UTF-8 terminal data without mojibake", () => {
    const text = "héllo Привет 你好\n";

    const bytes = decodeBase64Bytes(encodeTerminalInput(text));

    expect(new TextDecoder().decode(bytes)).toBe(text);
  });
});
