import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { APP_ERROR_CODES, appErrorPresentation } from "../../api/errors";
import { Button, DataTable, Modal, StatusDot, Tabs } from ".";

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
  });

  it("renders status text without relying on color alone", () => {
    render(<StatusDot label="Running" tone="ok" />);

    expect(screen.getByText("Running")).toBeInTheDocument();
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

  it("shows selected rows and bulk actions", async () => {
    const onToggle = vi.fn();

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
        onToggleRow={onToggle}
        rowLabel={(row) => row.name}
        rows={[{ name: "api" }]}
        selectedIDs={new Set(["api"])}
      />,
    );

    expect(screen.getByText("1 selected")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select api" }));
    expect(onToggle).toHaveBeenCalledWith("api");
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

  it("keeps the virtual table window inside shorter filtered row sets", () => {
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

    expect(screen.getByText("Row 129")).toBeInTheDocument();
  });
});
