import type { MetricRankItem } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

type DashboardStatsSample = {
  containerID: string;
  containerName?: string;
  cpuPercent: number;
  gpuMemoryBytes?: number;
  memoryBytes: number;
};

type ContainerStatusCounts = {
  paused: number;
  running: number;
  stopped: number;
  unhealthy: number;
};

export type ContainerStatusChartSegment = {
  color: string;
  filter: string;
  name: string;
  value: number;
};

export const chartColors = {
  axis: "rgb(var(--chart-axis))",
  cpu: "rgb(var(--chart-cpu))",
  gpu: "rgb(var(--chart-gpu))",
  grid: "rgb(var(--chart-grid) / 0.55)",
  memory: "rgb(var(--chart-memory))",
  networkRx: "rgb(var(--chart-network-rx))",
  networkTx: "rgb(var(--chart-network-tx))",
  paused: "rgb(var(--chart-paused))",
  running: "rgb(var(--chart-running))",
  spark: "rgb(var(--chart-spark))",
  stopped: "rgb(var(--chart-stopped))",
  unhealthy: "rgb(var(--chart-unhealthy))",
} as const;

export const emptyContainerStatusChartSegment: ContainerStatusChartSegment = {
  color: chartColors.stopped,
  filter: "all",
  name: "None",
  value: 1,
};

export function dashboardTopRows(
  dashboardTop: MetricRankItem[],
  latestSamples: Record<string, DashboardStatsSample>,
) {
  const liveRows = Object.values(latestSamples)
    .map(
      (sample): MetricRankItem => ({
        id: sample.containerID,
        name: sample.containerName || shortID(sample.containerID),
        kind: "container",
        cpuPercent: sample.cpuPercent,
        gpuMemoryBytes: sample.gpuMemoryBytes ?? 0,
        memoryBytes: sample.memoryBytes,
      }),
    )
    .sort(
      (left, right) =>
        (right.gpuMemoryBytes ?? 0) - (left.gpuMemoryBytes ?? 0) ||
        (right.cpuPercent ?? 0) - (left.cpuPercent ?? 0) ||
        (right.memoryBytes ?? 0) - (left.memoryBytes ?? 0),
    )
    .slice(0, 8);
  return liveRows.length > 0 ? liveRows : dashboardTop.slice(0, 8);
}

export function containerStatusChartSegments({
  paused,
  running,
  stopped,
  unhealthy,
}: ContainerStatusCounts): ContainerStatusChartSegment[] {
  return [
    {
      color: chartColors.running,
      filter: "running",
      name: "Running",
      value: running,
    },
    {
      color: chartColors.stopped,
      filter: "stopped",
      name: "Stopped",
      value: stopped,
    },
    {
      color: chartColors.unhealthy,
      filter: "unhealthy",
      name: "Unhealthy",
      value: unhealthy,
    },
    {
      color: chartColors.paused,
      filter: "paused",
      name: "Paused",
      value: paused,
    },
  ].filter((item) => item.value > 0);
}

function shortID(id: string) {
  if (id.length <= 12) {
    return id;
  }
  return id.slice(0, 12);
}
