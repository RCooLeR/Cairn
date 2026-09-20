import { describe, expect, it } from "vitest";

import { TestConsolePolicy } from "./consolePolicy";
import { allowConsoleWarningOnce } from "./setup";

describe("TestConsolePolicy", () => {
  it("routes the installed console interceptor through one-shot expectations", () => {
    allowConsoleWarningOnce(
      "the console-policy integration probe",
      (arguments_) => arguments_[0] === "expected integration warning",
    );

    console.warn("expected integration warning");
  });

  it("rejects unexpected warnings with actionable output", () => {
    const policy = new TestConsolePolicy();
    policy.record("warn", ["layout warning", { width: 0 }]);

    expect(() => policy.verifyAndReset()).toThrow(
      'unexpected console.warn: layout warning {"width":0}',
    );
  });

  it("allows exactly one narrowly matched intentional message", () => {
    const policy = new TestConsolePolicy();
    policy.allowOnce(
      "error",
      "the tested failure",
      (arguments_) => arguments_[0] === "expected failure",
    );
    policy.record("error", ["expected failure"]);

    expect(() => policy.verifyAndReset()).not.toThrow();
  });

  it("permits an explicitly allowlisted toolchain diagnostic", () => {
    const policy = new TestConsolePolicy();
    policy.allowDiagnostic(
      "warn",
      "known tool warning",
      (arguments_) => arguments_[0] === "known tool warning",
    );
    policy.record("warn", ["known tool warning"]);

    expect(() => policy.verifyAndReset()).not.toThrow();
  });

  it("rejects missing and surplus allowlisted calls", () => {
    const policy = new TestConsolePolicy();
    policy.allowOnce("error", "one expected failure", () => true);

    expect(() => policy.verifyAndReset()).toThrow(
      "expected one console.error call matching: one expected failure",
    );

    policy.allowOnce("error", "one expected failure", () => true);
    policy.record("error", ["first"]);
    policy.record("error", ["second"]);
    expect(() => policy.verifyAndReset()).toThrow(
      "unexpected console.error: second",
    );
  });
});
