import { render, screen, waitFor } from "@testing-library/react";
import { Line, LineChart, ResponsiveContainer } from "recharts";
import { describe, expect, it } from "vitest";

describe("chart test geometry", () => {
  it("renders responsive charts at their explicit container dimensions", async () => {
    render(
      <div style={{ height: "320px", width: "640px" }}>
        <ResponsiveContainer height="100%" width="100%">
          <LineChart
            accessibilityLayer
            data={[
              { time: 1, value: 4 },
              { time: 2, value: 7 },
            ]}
          >
            <Line dataKey="value" isAnimationActive={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>,
    );

    const chart = await screen.findByRole("application");
    await waitFor(() => {
      expect(chart).toHaveAttribute("width", "640");
      expect(chart).toHaveAttribute("height", "320");
    });
    expect(chart.querySelector(".recharts-line-curve")).toBeInTheDocument();
  });
});
