import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  APP_ERROR_CODES,
  appErrorPresentation,
  parseAppErrorText,
} from "../../api/errors";
import { useToastQueue } from "../../hooks/useToastQueue";
import {
  Button,
  DataTable,
  Modal,
  StatusDot,
  Tabs,
  ToastViewport,
  Tooltip,
} from ".";

describe("UI kit", () => {
  it("renders button loading and disabled states", () => {
    render(
      <Button disabledReason="Waiting for provider" loading>
        Start
      </Button>,
    );

    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Start" })).toHaveAttribute(
      "title",
      "Waiting for provider",
    );
    expect(screen.getByRole("button", { name: "Start" })).toHaveAttribute(
      "aria-busy",
      "true",
    );
    expect(screen.getByText("Waiting for provider")).toHaveClass("sr-only");
  });

  it("renders status text without relying on color alone", () => {
    render(<StatusDot label="Running" tone="ok" />);

    expect(screen.getByText("Running")).toBeInTheDocument();
  });

  it("queues and dismisses toast messages", () => {
    vi.useFakeTimers();

    function ToastHarness() {
      const { pushToast, toasts } = useToastQueue();
      return (
        <>
          <button
            onClick={() =>
              pushToast({
                body: "Saved to disk",
                level: "ok",
                title: "Setting saved",
              })
            }
            type="button"
          >
            Push
          </button>
          <ToastViewport toasts={toasts} />
        </>
      );
    }

    render(<ToastHarness />);
    fireEvent.click(screen.getByRole("button", { name: "Push" }));

    expect(screen.getByRole("status")).toHaveTextContent("Setting saved");

    act(() => {
      vi.advanceTimersByTime(3200);
    });
    expect(screen.queryByText("Setting saved")).not.toBeInTheDocument();

    vi.useRealTimers();
  });

  it("announces icon-only status dots", () => {
    render(<StatusDot tone="warn" />);

    expect(
      screen.getByRole("img", { name: "Status warning" }),
    ).toBeInTheDocument();
  });

  it("wraps long tooltip text within its max width", () => {
    render(
      <Tooltip label="A long tooltip label that should wrap instead of clipping">
        <button type="button">Hover me</button>
      </Tooltip>,
    );

    expect(screen.getByRole("tooltip")).toHaveClass("whitespace-normal");
    expect(screen.getByRole("tooltip")).toHaveClass("break-words");
  });

  it("switches tabs with buttons", async () => {
    const onChange = vi.fn();

    render(
      <Tabs
        activeID="overview"
        items={[
          { id: "overview", label: "Overview" },
          { id: "services", label: "Services" },
        ]}
        onChange={onChange}
      >
        Content
      </Tabs>,
    );

    fireEvent.click(screen.getByRole("tab", { name: "Services" }));
    expect(onChange).toHaveBeenCalledWith("services");

    fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowRight" });
    expect(onChange).toHaveBeenCalledWith("services");
  });

  it("keeps tab keyboard navigation working when the active tab is disabled", () => {
    const onChange = vi.fn();

    render(
      <Tabs
        activeID="overview"
        items={[
          { id: "overview", label: "Overview", disabled: true },
          { id: "services", label: "Services" },
          { id: "logs", label: "Logs" },
        ]}
        onChange={onChange}
      >
        Content
      </Tabs>,
    );

    expect(screen.getByRole("tab", { name: "Services" })).toHaveAttribute(
      "tabindex",
      "0",
    );
    fireEvent.keyDown(screen.getByRole("tablist"), { key: "ArrowRight" });
    expect(onChange).toHaveBeenCalledWith("logs");
  });

  it("sorts table rows by sortable columns", async () => {
    render(
      <DataTable
        columns={[
          {
            id: "name",
            header: "Name",
            render: (row: { name: string }) => row.name,
            sortable: true,
            sortValue: (row) => row.name,
          },
        ]}
        getRowID={(row) => row.name}
        ariaLabel="Workers"
        rows={[{ name: "worker" }, { name: "api" }]}
      />,
    );

    expect(screen.getByRole("table", { name: "Workers" })).toHaveAttribute(
      "aria-rowcount",
      "2",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Sort by Name, not sorted" }),
    );
    expect(screen.getAllByRole("cell").map((cell) => cell.textContent)).toEqual(
      ["api", "worker"],
    );
    expect(
      screen.getByRole("button", { name: "Sort by Name, sorted ascending" }),
    ).toBeInTheDocument();
  });

  it("shows and hides table columns from the header menu", () => {
    render(
      <DataTable
        columns={[
          {
            id: "name",
            header: "Name",
            render: (row: { name: string; status: string }) => row.name,
          },
          {
            id: "status",
            header: "Status",
            render: (row) => row.status,
          },
        ]}
        getRowID={(row) => row.name}
        ariaLabel="Workers"
        rows={[{ name: "api", status: "running" }]}
      />,
    );

    fireEvent.contextMenu(screen.getByRole("columnheader", { name: /Name/ }));
    fireEvent.click(screen.getByLabelText("Status"));

    expect(
      screen.queryByRole("columnheader", { name: /Status/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("cell", { name: "running" }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Show all columns" }));

    expect(
      screen.getByRole("columnheader", { name: /Status/ }),
    ).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "running" })).toBeInTheDocument();
  });

  it("resizes table columns from the header grip", () => {
    const { container } = render(
      <DataTable
        columns={[
          {
            id: "name",
            header: "Name",
            render: (row: { name: string }) => row.name,
          },
        ]}
        getRowID={(row) => row.name}
        ariaLabel="Workers"
        rows={[{ name: "api" }]}
      />,
    );

    const grip = screen.getByRole("separator", {
      name: "Resize Name column",
    });

    fireEvent.mouseDown(grip, { button: 0, clientX: 100 });
    fireEvent.mouseMove(window, { clientX: 160 });
    fireEvent.mouseUp(window);

    expect(container.querySelector('[data-column-id="name"]')).toHaveStyle({
      width: "240px",
    });
  });

  it("shows selected rows and bulk actions", async () => {
    const onToggle = vi.fn();
    const onToggleAll = vi.fn();

    render(
      <DataTable
        bulkActions={<Button size="sm">Stop</Button>}
        columns={[
          {
            id: "name",
            header: "Name",
            render: (row: { name: string }) => row.name,
          },
        ]}
        getRowID={(row) => row.name}
        onToggleAllRows={onToggleAll}
        onToggleRow={onToggle}
        rowLabel={(row) => row.name}
        rows={[{ name: "api" }, { name: "worker" }]}
        selectedIDs={new Set(["api"])}
      />,
    );

    expect(screen.getByText("1 selected")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select api" }));
    expect(onToggle).toHaveBeenCalledWith("api");
    fireEvent.click(screen.getByRole("checkbox", { name: "Select all rows" }));
    expect(onToggleAll).toHaveBeenCalledWith(["api", "worker"], true);
  });

  it("focuses modal panel unless an autofocus target is present", () => {
    const onClose = vi.fn();

    render(
      <Modal onClose={onClose} open title="Confirm">
        Body
      </Modal>,
    );

    expect(screen.getByRole("dialog", { name: "Confirm" })).toHaveFocus();
  });

  it("constrains modal content to an internal scroll area", () => {
    const onClose = vi.fn();

    render(
      <Modal onClose={onClose} open title="Long repair plan">
        <div>Scrollable modal content</div>
      </Modal>,
    );

    const dialog = screen.getByRole("dialog", { name: "Long repair plan" });
    const content = screen.getByText("Scrollable modal content").parentElement;

    expect(dialog).toHaveClass("max-h-[calc(100vh-2.5rem)]");
    expect(dialog).toHaveClass("overflow-hidden");
    expect(content).toHaveClass("overflow-y-auto");
    expect(content).toHaveClass("overscroll-contain");
  });

  it("does not steal input focus when an open modal rerenders", () => {
    const modal = (onClose: () => void) => (
      <Modal onClose={onClose} open title="Registry Login">
        <label>
          Registry
          <select defaultValue="docker.io">
            <option value="docker.io">Docker Hub</option>
          </select>
        </label>
        <label>
          Secret
          <input aria-label="Secret" type="password" />
        </label>
      </Modal>
    );
    const { rerender } = render(modal(vi.fn()));

    const secret = screen.getByLabelText("Secret");
    secret.focus();
    expect(secret).toHaveFocus();

    rerender(modal(vi.fn()));

    expect(secret).toHaveFocus();
  });

  it("closes modal on Escape", async () => {
    const onClose = vi.fn();

    render(
      <Modal onClose={onClose} open title="Confirm">
        Body
      </Modal>,
    );

    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  it("keeps busy modal open on Escape", async () => {
    const onClose = vi.fn();

    render(
      <Modal busy onClose={onClose} open title="Confirm">
        Body
      </Modal>,
    );

    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
  });

  it("maps every contract AppError code to a UI surface", () => {
    expect(APP_ERROR_CODES).toHaveLength(17);
    for (const code of APP_ERROR_CODES) {
      const presentation = appErrorPresentation(code);
      expect(presentation.title).toBeTruthy();
      expect(presentation.body).toBeTruthy();
      expect(presentation.surface).toMatch(
        /^(global|inline|modal|permission|row|toast)$/,
      );
    }
    expect(appErrorPresentation("E_PERMISSION_DENIED").surface).toBe(
      "permission",
    );
    expect(appErrorPresentation("E_NOT_FOUND").surface).toBe("toast");
  });

  it("turns structured app errors into readable UI copy", () => {
    const parsed = parseAppErrorText(
      JSON.stringify({
        message: "E_COMPOSE_INVALID: Compose project action failed",
        cause: {
          code: "E_COMPOSE_INVALID",
          message: "Compose project action failed",
          detail: "docker compose pull failed",
        },
      }),
    );

    expect(parsed.title).toBe("Compose file is invalid");
    expect(parsed.body).toBe("Compose project action failed");
    expect(parsed.detail).toBe("docker compose pull failed");
  });

  it("windows large table row sets for seed-scale inventory pages", () => {
    const rows = Array.from({ length: 200 }, (_, index) => ({
      id: `row-${index}`,
      label: `Row ${index}`,
    }));

    render(
      <DataTable
        columns={[
          {
            id: "label",
            header: "Label",
            render: (row) => row.label,
          },
        ]}
        getRowID={(row) => row.id}
        rows={rows}
      />,
    );

    expect(screen.getByText("Row 0")).toBeInTheDocument();
    expect(screen.queryByText("Row 199")).not.toBeInTheDocument();
  });

  it("resets the virtual table window when row sets change", () => {
    const makeRows = (count: number) =>
      Array.from({ length: count }, (_, index) => ({
        id: `row-${index}`,
        label: `Row ${index}`,
      }));
    const table = (rows: ReturnType<typeof makeRows>) => (
      <DataTable
        columns={[
          {
            id: "label",
            header: "Label",
            render: (row) => row.label,
          },
        ]}
        getRowID={(row) => row.id}
        rows={rows}
      />
    );

    const { rerender } = render(table(makeRows(200)));
    fireEvent.scroll(screen.getByRole("table").parentElement as HTMLElement, {
      target: { scrollTop: 8000 },
    });

    rerender(table(makeRows(130)));

    expect(screen.getByText("Row 0")).toBeInTheDocument();
    expect(screen.queryByText("Row 129")).not.toBeInTheDocument();
  });
});
