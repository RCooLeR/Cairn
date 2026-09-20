import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { CleanupModal, emptyCleanup } from "./CleanupModal";

it("locks the displayed cleanup selection and confirmation while it is running", () => {
  const onChange = vi.fn();
  render(
    <CleanupModal
      onChange={onChange}
      onClose={vi.fn()}
      onConfirm={vi.fn()}
      reclaimableLabel="1 GB"
      state={{ ...emptyCleanup, open: true, busy: true, typedName: "prune" }}
    />,
  );
  for (const checkbox of screen.getAllByRole("checkbox")) {
    expect(checkbox).toBeDisabled();
  }
  expect(screen.getByRole("textbox")).toBeDisabled();
  expect(screen.getByRole("button", { name: "Clean up" })).toBeDisabled();
});
