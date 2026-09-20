import React from "react";

type ErrorBoundaryProps = {
  children: React.ReactNode;
};

type ErrorBoundaryState = {
  errorMessage: string | null;
};

export class ErrorBoundary extends React.Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  state: ErrorBoundaryState = { errorMessage: null };

  static getDerivedStateFromError(error: unknown): ErrorBoundaryState {
    const message =
      error instanceof Error && error.message.trim() !== ""
        ? error.message
        : "An unexpected UI error occurred.";
    return { errorMessage: message };
  }

  componentDidCatch(error: unknown, info: React.ErrorInfo) {
    console.error("Cairn UI render failure", error, info.componentStack);
  }

  render() {
    if (this.state.errorMessage) {
      return (
        <main className="flex min-h-screen items-center justify-center bg-bg-app px-6 text-text-primary">
          <section
            aria-live="assertive"
            className="w-full max-w-lg rounded-card border border-border bg-bg-panel p-6 shadow-sm"
            role="alert"
          >
            <p className="text-sm font-semibold text-error">
              Something went wrong
            </p>
            <h1 className="mt-2 text-xl font-semibold">Cairn hit a UI error</h1>
            <p className="mt-3 text-sm text-text-secondary">
              Reload the window to recover. The app state on disk is preserved.
            </p>
            <pre className="mt-4 max-h-32 overflow-auto rounded-control border border-border bg-bg-inset px-3 py-2 text-xs text-text-secondary">
              {this.state.errorMessage}
            </pre>
            <button
              className="mt-5 rounded-control border border-accent bg-accent px-4 py-2 text-sm font-semibold text-bg-app shadow-sm hover:bg-accent/90"
              onClick={() => window.location.reload()}
              type="button"
            >
              Reload Cairn
            </button>
          </section>
        </main>
      );
    }

    return this.props.children;
  }
}
