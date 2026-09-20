export type ConsoleLevel = "error" | "warn";

export type ConsoleMessageMatcher = (arguments_: readonly unknown[]) => boolean;

type ConsoleCall = {
  arguments_: readonly unknown[];
  level: ConsoleLevel;
};

type ConsoleExpectation = {
  description: string;
  level: ConsoleLevel;
  matcher: ConsoleMessageMatcher;
};

/**
 * Records warning/error output so a passing test cannot leave diagnostics hidden
 * in CI output. Tests that deliberately exercise logging must opt in to one exact
 * call at a time; every expectation is also required to be observed.
 */
export class TestConsolePolicy {
  private readonly allowlistedMessages: ConsoleExpectation[] = [];
  private readonly calls: ConsoleCall[] = [];
  private readonly expectations: ConsoleExpectation[] = [];

  allowDiagnostic(
    level: ConsoleLevel,
    description: string,
    matcher: ConsoleMessageMatcher,
  ) {
    this.allowlistedMessages.push({ description, level, matcher });
  }

  allowOnce(
    level: ConsoleLevel,
    description: string,
    matcher: ConsoleMessageMatcher,
  ) {
    this.expectations.push({ description, level, matcher });
  }

  record(level: ConsoleLevel, arguments_: readonly unknown[]) {
    if (
      this.allowlistedMessages.some(
        (message) => message.level === level && message.matcher(arguments_),
      )
    ) {
      return;
    }
    this.calls.push({ arguments_, level });
  }

  verifyAndReset() {
    const calls = this.calls.splice(0);
    const expectations = this.expectations.splice(0);
    const unexpected: ConsoleCall[] = [];

    for (const call of calls) {
      const expectationIndex = expectations.findIndex(
        (expectation) =>
          expectation.level === call.level &&
          expectation.matcher(call.arguments_),
      );
      if (expectationIndex >= 0) {
        expectations.splice(expectationIndex, 1);
      } else {
        unexpected.push(call);
      }
    }

    if (unexpected.length === 0 && expectations.length === 0) return;

    const details = [
      ...unexpected.map(
        ({ arguments_, level }) =>
          `unexpected console.${level}: ${formatArguments(arguments_)}`,
      ),
      ...expectations.map(
        ({ description, level }) =>
          `expected one console.${level} call matching: ${description}`,
      ),
    ];
    throw new Error(
      `Test emitted invalid console output:\n${details.join("\n")}`,
    );
  }
}

function formatArguments(arguments_: readonly unknown[]) {
  return arguments_
    .map((argument) => {
      if (argument instanceof Error) {
        return `${argument.name}: ${argument.message}`;
      }
      if (typeof argument === "string") return argument;
      try {
        return JSON.stringify(argument);
      } catch {
        return String(argument);
      }
    })
    .join(" ");
}
