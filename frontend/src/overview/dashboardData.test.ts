import { describe, expect, it } from "vitest";

import type { MetricRankItem } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  chartColors,
  containerStatusChartSegments,
  dashboardTopRows,
  emptyContainerStatusChartSegment,
} from "./dashboardData";

function rankItem(
  id: string,
  cpuPercent: number,
  memoryBytes: number,
): MetricRankItem {
  return {
    cpuPercent,
    id,
    kind: "container",
    memoryBytes,
    name: id,
  };
}

describe("dashboardTopRows", () => {
  it("uses live samples sorted by CPU then memory before stored dashboard rows", () => {
    const fallback = [rankItem("fallback", 99, 99)];

    expect(
      dashboardTopRows(fallback, {
        highMemory: {
          containerID: "highMemory",
          containerName: "High memory",
          cpuPercent: 10,
          memoryBytes: 2048,
        },
        highCpu: {
          containerID: "highCpu",
          cpuPercent: 42,
          memoryBytes: 1024,
        },
        lowMemory: {
          containerID: "lowMemory",
          cpuPercent: 10,
          memoryBytes: 512,
        },
      }),
    ).toEqual([
      rankItem("highCpu", 42, 1024),
      {
        cpuPercent: 10,
        id: "highMemory",
        kind: "container",
        memoryBytes: 2048,
        name: "High memory",
      },
      rankItem("lowMemory", 10, 512),
    ]);
  });

  it("falls back to the stored top rows and limits output", () => {
    const fallback = Array.from({ length: 10 }, (_, index) =>
      rankItem(`stored-${index}`, index, index),
    );

    expect(dashboardTopRows(fallback, {})).toHaveLength(8);
    expect(dashboardTopRows(fallback, {})[0]?.id).toBe("stored-0");
  });
});

describe("chartColors", () => {
  it("references theme CSS variables instead of fixed colors", () => {
    expect(chartColors.cpu).toBe("rgb(var(--chart-cpu))");
    expect(chartColors.grid).toBe("rgb(var(--chart-grid) / 0.55)");
    expect(Object.values(chartColors).join(" ")).not.toMatch(
      /#[0-9a-f]{3,8}|rgba\(/i,
    );
  });
});

describe("containerStatusChartSegments", () => {
  it("returns only visible status segments with theme colors", () => {
    expect(
      containerStatusChartSegments({
        paused: 0,
        running: 2,
        stopped: 0,
        unhealthy: 1,
      }),
    ).toEqual([
      {
        color: chartColors.running,
        filter: "running",
        name: "Running",
        value: 2,
      },
      {
        color: chartColors.unhealthy,
        filter: "unhealthy",
        name: "Unhealthy",
        value: 1,
      },
    ]);
  });

  it("uses the stopped theme color for the empty segment", () => {
    expect(emptyContainerStatusChartSegment).toEqual({
      color: chartColors.stopped,
      filter: "all",
      name: "None",
      value: 1,
    });
  });
});
