type RuntimeEvent = {
  name: string;
  data?: unknown;
};

type RuntimeCallback = (event: RuntimeEvent) => void;

const listeners = new Map<string, Set<RuntimeCallback>>();
const clipboardWrites: string[] = [];
let terminalCounter = 1;

export class CancellablePromise<T> extends Promise<T> {}

export const Create = {
  Any: (value: unknown) => value,
  Nullable:
    <T>(create: (value: unknown) => T) =>
    (value: unknown): T | null =>
      value === null || value === undefined ? null : create(value),
  Array:
    <T>(create: (value: unknown) => T) =>
    (value: unknown): T[] =>
      Array.isArray(value) ? value.map((item) => create(item)) : [],
  Map:
    <TKey extends string | number | symbol, TValue>(
      createKey: (value: unknown) => TKey,
      createValue: (value: unknown) => TValue,
    ) =>
    (value: unknown): Record<string, TValue> => {
      if (!value || typeof value !== "object") {
        return {};
      }
      return Object.fromEntries(
        Object.entries(value).map(([key, entry]) => [
          String(createKey(key)),
          createValue(entry),
        ]),
      );
    },
  Events: {},
};

export const Events = {
  On(name: string, callback: RuntimeCallback) {
    const callbacks = listeners.get(name) ?? new Set<RuntimeCallback>();
    callbacks.add(callback);
    listeners.set(name, callbacks);
    return () => {
      callbacks.delete(callback);
    };
  },
  Emit(name: string, data?: unknown) {
    listeners.get(name)?.forEach((callback) => callback({ name, data }));
  },
};

// The generated Wails event hook references Events.* types, so the mock keeps
// the same namespace shape for release-validation builds.
// eslint-disable-next-line @typescript-eslint/no-namespace
export namespace Events {
  export type WailsEventName = string;
  export type WailsEventCallback<T extends WailsEventName> = (
    event: RuntimeEvent & { name: T },
  ) => void;
}

export const Clipboard = {
  async SetText(text: string) {
    clipboardWrites.push(text);
  },
};

export const Browser = {
  async OpenURL(url: string | URL) {
    void url;
    return undefined;
  },
};

export const Dialogs = {
  async OpenFile() {
    return "/home/cairn/projects/app-db";
  },
  async SaveFile() {
    return "/tmp/cairn-logs.jsonl";
  },
};

export const Call = {
  ByID(id: number, ...args: unknown[]) {
    recordCall(id);
    try {
      const handler = callHandlers[id];
      const result = handler ? handler(...args) : null;
      return Promise.resolve(result);
    } catch (error) {
      return Promise.reject(error);
    }
  },
};

const health = {
  healthy: "healthy",
  unknown: "unknown",
};

const projectStatus = {
  running: "running",
  stopped: "stopped",
};

const update = {
  serviceImage: "service_image",
  baseImage: "base_image",
  serviceImageAvailable: "service_image_update_available",
  rebuildRequired: "rebuild_required",
  unknownBase: "unknown_base_image",
  ignored: "ignored",
  upToDate: "up_to_date",
  high: "high",
  unknown: "unknown",
  pullRecreate: "pull_recreate",
  rebuildRedeploy: "rebuild_redeploy",
  manual: "manual",
};

const risk = {
  safe: "safe",
  confirm: "needs_confirmation",
  destructive: "destructive",
  dangerous: "dangerous",
};

function providerStatus() {
  const degraded = isDegradedFixture();
  return {
    installed: true,
    running: !degraded,
    healthy: !degraded,
    dockerInstalled: true,
    dockerRunning: !degraded,
    composeInstalled: true,
    buildxInstalled: true,
    dockerVersion: "26.1.0",
    composeVersion: "v2.27.0",
    currentContext: "default",
  };
}

function releaseFixtureMode() {
  try {
    return globalThis.localStorage?.getItem("cairn.release.fixture") ?? "";
  } catch {
    return "";
  }
}

function isDegradedFixture() {
  return releaseFixtureMode() === "degraded";
}

function isSeededFixture() {
  return releaseFixtureMode() === "seeded";
}

function dockerStopped<T>(value: T): T | Promise<never> {
  return isDegradedFixture()
    ? Promise.reject(new Error("Docker daemon ping failed"))
    : value;
}

const callNames: Record<number, string> = {
  2194297540: "AgentService.ToolCatalog",
  3181999705: "AgentService.Status",
  3444129041: "AgentService.Chat",
  4257326101: "SettingsService.SetSetting",
  1752754799: "DockerService.StopContainer",
  3715102761: "LogsService.StartLogStream",
  48603856: "MetricsService.StartStatsStream",
};

function recordCall(id: number) {
  const state = globalThis as unknown as {
    __cairnReleaseMockCalls?: Record<string, number>;
  };
  const calls = state.__cairnReleaseMockCalls ?? {};
  const name = callNames[id] ?? String(id);
  calls[name] = (calls[name] ?? 0) + 1;
  state.__cairnReleaseMockCalls = calls;
}

const container = {
  id: "container-1",
  name: "web",
  image: "cairn/web:latest",
  imageID: "sha256:image-1",
  status: "Up 2 minutes",
  state: "running",
  health: health.healthy,
  projectID: "linux_native/app-db",
  service: "web",
  ports: [
    {
      hostIP: "127.0.0.1",
      hostPort: "8080",
      containerPort: "80",
      protocol: "tcp",
    },
  ],
  cpuPercent: 2.4,
  memoryBytes: 64 * 1024 * 1024,
  memoryLimit: 512 * 1024 * 1024,
  restarts: 0,
  createdAt: "2026-06-13T08:00:00Z",
};

const volume = {
  name: "cairn_data",
  driver: "local",
  mountpoint: "/var/lib/docker/volumes/cairn_data/_data",
  labels: { "com.docker.compose.project": "app-db" },
  sizeBytes: 2048,
  inUse: true,
};

const network = {
  id: "network-1",
  name: "cairn_default",
  driver: "bridge",
  scope: "local",
  internal: false,
  attachable: true,
  labels: { "com.docker.compose.project": "app-db" },
};

const image = {
  id: "sha256:image-1",
  repoTags: ["cairn/web:latest"],
  repoDigests: ["cairn/web@sha256:digest"],
  sizeBytes: 128 * 1024 * 1024,
  createdAt: "2026-06-12T08:00:00Z",
  inUse: true,
  updateStatus: update.upToDate,
};

const seededCounts = {
  containers: 100,
  images: 500,
  networks: 20,
  projects: 10,
  volumes: 200,
};

function diskCategory(count: number, active: number, sizeBytes: number) {
  return { count, active, sizeBytes, reclaimable: 0 };
}

function padded(value: number, width: number) {
  return String(value).padStart(width, "0");
}

function seededProjectID(index: number) {
  return `linux_native/project-${index}`;
}

function seededProjects() {
  return Array.from({ length: seededCounts.projects }, (_, index) => ({
    id: seededProjectID(index),
    name: `project-${index}`,
    providerID: "linux_native",
    status: projectStatus.running,
    health: index % 6 === 0 ? health.unknown : health.healthy,
    servicesRunning: 8,
    servicesTotal: 10,
    cpuPercent: 10 + index,
    memoryBytes: (192 + index * 8) * 1024 * 1024,
    netRxRate: index * 512,
    netTxRate: index * 384,
    updateBadges: {
      imageUpdates: index % 3,
      baseUpdates: index % 2,
      rebuildNeeded: index % 4 === 0 ? 1 : 0,
      pinned: index % 5 === 0 ? 1 : 0,
      unknownBase: index % 7 === 0 ? 1 : 0,
    },
    ports: [
      {
        hostIP: "127.0.0.1",
        hostPort: String(8400 + index),
        containerPort: "80",
        protocol: "tcp",
      },
    ],
    workingDir: `/home/cairn/projects/project-${index}`,
    lastChangedAt: "2026-06-13T08:00:00Z",
  }));
}

function seededContainers() {
  return Array.from({ length: seededCounts.containers }, (_, index) => {
    const projectIndex = index % seededCounts.projects;
    const running = index % 5 !== 0;
    return {
      id: `container-${padded(index, 3)}`,
      name: `service-${padded(index, 3)}`,
      image: `cairn/repo-${padded(index % seededCounts.images, 3)}:latest`,
      imageID: `sha256:image-${padded(index, 3)}`,
      status: running ? `Up ${index + 1} minutes` : "Exited (0) 1 hour ago",
      state: running ? "running" : "exited",
      health: running && index % 9 !== 0 ? health.healthy : health.unknown,
      projectID: seededProjectID(projectIndex),
      service: `service-${padded(index % 10, 2)}`,
      ports:
        index % 3 === 0
          ? [
              {
                hostIP: "127.0.0.1",
                hostPort: String(8000 + index),
                containerPort: "80",
                protocol: "tcp",
              },
            ]
          : [],
      cpuPercent: Number((index % 20) + 0.5),
      memoryBytes: (64 + (index % 12) * 16) * 1024 * 1024,
      memoryLimit: 512 * 1024 * 1024,
      restarts: index % 4,
      createdAt: "2026-06-13T08:00:00Z",
    };
  });
}

function seededImages() {
  return Array.from({ length: seededCounts.images }, (_, index) => ({
    id: `sha256:image-${padded(index, 3)}`,
    repoTags: index % 17 === 0 ? [] : [`cairn/repo-${padded(index, 3)}:latest`],
    repoDigests: [`cairn/repo-${padded(index, 3)}@sha256:digest-${index}`],
    sizeBytes: (24 + (index % 48)) * 1024 * 1024,
    createdAt: "2026-06-12T08:00:00Z",
    inUse: index < seededCounts.containers,
    updateStatus:
      index % 19 === 0 ? update.serviceImageAvailable : update.upToDate,
  }));
}

function seededVolumes() {
  return Array.from({ length: seededCounts.volumes }, (_, index) => ({
    name: `volume-${padded(index, 3)}`,
    driver: "local",
    mountpoint: `/var/lib/docker/volumes/volume-${padded(index, 3)}/_data`,
    labels: {
      "com.docker.compose.project": `project-${index % seededCounts.projects}`,
    },
    sizeBytes: (1 + (index % 10)) * 1024 * 1024,
    inUse: index % 4 !== 0,
  }));
}

function seededNetworks() {
  return Array.from({ length: seededCounts.networks }, (_, index) => ({
    id: `network-${padded(index, 2)}`,
    name: `network-${padded(index, 2)}`,
    driver: "bridge",
    scope: "local",
    internal: index % 10 === 0,
    attachable: true,
    labels: {
      "com.docker.compose.project": `project-${index % seededCounts.projects}`,
    },
  }));
}

function projectSummary() {
  return {
    id: "linux_native/app-db",
    name: "app-db",
    providerID: "linux_native",
    status: projectStatus.running,
    health: health.healthy,
    servicesRunning: 1,
    servicesTotal: 2,
    cpuPercent: 12.5,
    memoryBytes: 256 * 1024 * 1024,
    netRxRate: 0,
    netTxRate: 0,
    updateBadges: {
      imageUpdates: 2,
      baseUpdates: 0,
      rebuildNeeded: 1,
      pinned: 0,
      unknownBase: 1,
    },
    ports: [container.ports[0]],
    workingDir: "/home/cairn/projects/app-db",
    lastChangedAt: "2026-06-13T08:00:00Z",
  };
}

function projectDetail() {
  return {
    summary: projectSummary(),
    services: [
      {
        name: "app",
        image: "cairn/app:latest",
        replicas: 1,
        running: 1,
        status: projectStatus.running,
        health: health.healthy,
        ports: [container.ports[0]],
        cpuPercent: 10,
        memoryBytes: 128 * 1024 * 1024,
      },
      {
        name: "db",
        image: "postgres:16",
        replicas: 1,
        running: 0,
        status: projectStatus.stopped,
        health: health.unknown,
      },
    ],
    containers: [
      {
        ...container,
        id: "container-app",
        name: "container-app",
        image: "cairn/app:latest",
        imageID: "sha256:image-app",
        service: "app",
      },
    ],
    compose: {
      rawFiles: [
        {
          path: "/home/cairn/projects/app-db/compose.yaml",
          content: "services:\n  app:\n    image: cairn/app:latest\n",
        },
      ],
      resolvedYAML:
        "services:\n  app:\n    image: cairn/app:latest\n  db:\n    image: postgres:16\n",
      envFiles: ["/home/cairn/projects/app-db/.env"],
      valid: true,
      errors: [],
    },
  };
}

function dashboardMetrics() {
  return {
    projects: 1,
    containers: 1,
    images: 1,
    volumes: 1,
    diskUsage: diskUsage(),
    top: [
      {
        id: "container-1",
        name: "web",
        kind: "container",
        cpuPercent: 2.4,
        memoryBytes: 64 * 1024 * 1024,
      },
    ],
    recentEvents: auditEntries(),
  };
}

function seededDashboardMetrics() {
  const containers = seededContainers();
  return {
    projects: seededCounts.projects,
    containers: seededCounts.containers,
    images: seededCounts.images,
    volumes: seededCounts.volumes,
    diskUsage: seededDiskUsage(),
    top: containers.slice(0, 8).map((item) => ({
      id: item.id,
      name: item.name,
      kind: "container",
      cpuPercent: item.cpuPercent,
      memoryBytes: item.memoryBytes,
    })),
    recentEvents: auditEntries(),
  };
}

function diskUsage() {
  return {
    images: diskCategory(1, 1, 128 * 1024 * 1024),
    containers: diskCategory(1, 1, 8 * 1024 * 1024),
    volumes: diskCategory(1, 1, 2048),
    buildCache: diskCategory(0, 0, 0),
    totalBytes: 136 * 1024 * 1024,
    reclaimable: 4 * 1024 * 1024,
  };
}

function seededDiskUsage() {
  const imagesBytes = 19 * 1024 * 1024 * 1024;
  const containerBytes = 2 * 1024 * 1024 * 1024;
  const volumeBytes = 8 * 1024 * 1024 * 1024;
  const cacheBytes = 3 * 1024 * 1024 * 1024;
  return {
    images: diskCategory(
      seededCounts.images,
      seededCounts.containers,
      imagesBytes,
    ),
    containers: diskCategory(
      seededCounts.containers,
      seededCounts.containers,
      containerBytes,
    ),
    volumes: diskCategory(
      seededCounts.volumes,
      seededCounts.volumes - 50,
      volumeBytes,
    ),
    buildCache: diskCategory(24, 8, cacheBytes),
    totalBytes: imagesBytes + containerBytes + volumeBytes + cacheBytes,
    reclaimable: 4 * 1024 * 1024 * 1024,
  };
}

function updates() {
  return [
    {
      id: 101,
      projectID: "linux_native/app-db",
      service: "app",
      containerID: "container-app",
      kind: update.serviceImage,
      status: update.serviceImageAvailable,
      currentImage: "cairn/app:latest",
      localDigest: "sha256:aaa111",
      remoteDigest: "sha256:bbb222",
      confidence: update.high,
      recommendedAction: update.pullRecreate,
      checkedAt: "2026-06-13T09:00:00Z",
      notes: ["Mutable tag warning"],
    },
    {
      id: 102,
      projectID: "linux_native/app-db",
      service: "worker",
      containerID: "container-worker",
      kind: update.baseImage,
      status: update.rebuildRequired,
      currentImage: "cairn/worker:local",
      baseImage: "node:20-alpine",
      localDigest: "sha256:ccc333",
      remoteDigest: "sha256:ddd444",
      confidence: update.high,
      recommendedAction: update.rebuildRedeploy,
      checkedAt: "2026-06-13T09:01:00Z",
    },
    {
      id: 103,
      projectID: "linux_native/app-db",
      service: "third-party",
      kind: update.baseImage,
      status: update.unknownBase,
      currentImage: "postgres:16",
      confidence: update.unknown,
      recommendedAction: update.manual,
      checkedAt: "2026-06-13T09:02:00Z",
    },
  ];
}

function ignoredUpdates() {
  return [{ ...updates()[0], id: 201, status: update.ignored }];
}

function updatePlan() {
  return {
    planID: "plan-update-project",
    projectID: "linux_native/app-db",
    items: [
      {
        service: "app",
        kind: update.serviceImage,
        currentImage: "cairn/app:latest",
        localDigest: "sha256:aaa111",
        remoteDigest: "sha256:bbb222",
        confidence: update.high,
        action: update.pullRecreate,
      },
    ],
    commands: [
      {
        order: 1,
        command: "docker compose pull app",
        risk: risk.confirm,
        explanation: "Pull updated service image.",
      },
      {
        order: 2,
        command: "docker compose up -d app",
        risk: risk.confirm,
        explanation: "Recreate service with the pulled image.",
      },
    ],
    warnings: [],
  };
}

function commandPlan(title: string, command: string, planRisk = risk.confirm) {
  return {
    planID: `plan-${title.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`,
    title,
    risk: planRisk,
    commands: [
      {
        order: 1,
        command,
        risk: planRisk,
        explanation: title,
      },
    ],
    effects: [title],
    requiresTypedName: planRisk === risk.dangerous ? "app-db" : undefined,
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function backups() {
  return [
    {
      id: "backup-1",
      providerID: "linux_native",
      volumeName: "cairn_data",
      projectID: "linux_native/app-db",
      path: "/tmp/cairn-backups/cairn_data-20260613T080000Z.tar.gz",
      metadataPath: "/tmp/cairn-backups/cairn_data-20260613T080000Z.json",
      sizeBytes: 4096,
      result: "success",
      createdAt: "2026-06-13T08:00:00Z",
    },
  ];
}

function lineage() {
  return [
    {
      projectID: "linux_native/app-db",
      service: "worker",
      containerID: "container-worker",
      imageRef: "cairn/worker:local",
      imageID: "sha256:image-worker",
      baseImage: "node:20-alpine",
      baseDigest: "sha256:ccc333",
      source: "compose_dockerfile",
      confidence: update.high,
      reason: "from Compose build config and Dockerfile",
    },
  ];
}

function auditEntries() {
  return [
    {
      id: 10,
      ts: "2026-06-13T09:00:00Z",
      action: "update.apply",
      target: "linux_native/app-db",
      result: "success",
      metadata: {
        command: "docker compose up -d",
        durationMS: 2000,
        projectID: "linux_native/app-db",
        providerID: "linux_native",
        risk: risk.confirm,
        targetType: "project",
      },
    },
    {
      id: 9,
      ts: "2026-06-13T08:55:00Z",
      action: "container.start",
      target: "web",
      result: "success",
      metadata: {
        command: "docker start web",
        projectID: "linux_native/app-db",
        risk: risk.safe,
        targetType: "container",
      },
    },
  ];
}

function terminalSession(kind = "host") {
  const id = `terminal-${terminalCounter++}`;
  return {
    id,
    kind,
    title: kind === "container" ? "web" : "Host",
    shell: "sh",
    isRoot: false,
    createdAt: "2026-06-13T08:00:00Z",
  };
}

function seededLogLines(count: number) {
  return Array.from({ length: count }, (_, index) => ({
    ts: `2026-06-13T09:${padded(Math.floor(index / 60) % 60, 2)}:${padded(index % 60, 2)}Z`,
    containerID: `container-${padded(index % seededCounts.containers, 3)}`,
    containerName: `service-${padded(index % seededCounts.containers, 3)}`,
    service: `service-${padded(index % 10, 2)}`,
    stream: "stdout",
    level: "info",
    text: `INFO release validation log line ${padded(index, 4)}`,
  }));
}

function callListCurrentUpdates(filter?: { status?: string[] }) {
  if (filter?.status?.includes(update.ignored)) {
    return ignoredUpdates();
  }
  return updates();
}

const callHandlers: Record<number, (...args: unknown[]) => unknown> = {
  3444129041: () => ({
    message:
      "The local agent mock reviewed the selected Docker context. Check Compose ports, image tags, health checks, and recent logs before applying changes.",
    toolResults: [
      {
        toolID: "docker.engine",
        title: "Docker engine summary",
        summary: "Mock engine context",
        data: JSON.stringify({ serverVersion: "26.1.0" }, null, 2),
      },
    ],
    model: "qwen2.5-coder:7b",
  }),
  3181999705: () => ({
    enabled: true,
    provider: "ollama",
    endpoint: "http://127.0.0.1:11434",
    model: "qwen2.5-coder:7b",
    reachable: true,
    availableModels: ["qwen2.5-coder:7b", "llama3.1:8b"],
    candidateModels: ["gemma4:12b", "qwen2.5-coder:7b", "llama3.1:8b"],
  }),
  2194297540: () => [
    {
      id: "docker.engine",
      name: "Docker engine summary",
      description: "Docker info, version, and disk usage.",
      readOnly: true,
    },
    {
      id: "docker.projects",
      name: "Compose projects",
      description: "Known Compose projects and their status badges.",
      readOnly: true,
    },
    {
      id: "container.logs",
      name: "Logs",
      description: "Recent selected project or container logs.",
      readOnly: true,
    },
  ],
  149806977: backups,
  2274904588: () => "backup-job",
  2356649536: () => "restore-job",
  3500563541: () =>
    commandPlan("Back up cairn_data", "docker run --rm backup helper"),
  2302725703: () =>
    commandPlan(
      "Restore cairn_data",
      "docker run --rm restore helper",
      risk.dangerous,
    ),
  1412705912: () => projectDetail().compose,
  3970934603: () => projectDetail().services,
  169789409: (ids) => ({
    total: Array.isArray(ids) ? ids.length : 1,
    ok: Array.isArray(ids) ? ids.length : 1,
    failed: 0,
    items: [],
  }),
  3605792666: () => network,
  500118806: () => volume,
  2345440464: () =>
    dockerStopped(isSeededFixture() ? seededDiskUsage() : diskUsage()),
  3255901267: () => ({
    summary: container,
    command: ["nginx", "-g", "daemon off;"],
    env: [{ key: "APP_ENV", value: "release" }],
    mounts: [],
    networks: [network.name],
    labels: { "com.docker.compose.project": "app-db" },
  }),
  975398329: () => ({
    summary: image,
    labels: { "org.opencontainers.image.source": "cairn" },
  }),
  884180562: () => ({
    summary: network,
    subnet: "172.19.0.0/16",
    gateway: "172.19.0.1",
    containers: [
      {
        ...container,
        networkName: network.name,
        endpointID: "endpoint-1",
        ipv4Address: "172.19.0.2/16",
        gateway: "172.19.0.1",
        macAddress: "02:42:ac:13:00:02",
        aliases: ["web", "app-web"],
      },
    ],
  }),
  3594940286: () => ({ summary: volume, containers: [container] }),
  364380892: () =>
    dockerStopped({
      id: "engine-1",
      name: "cairn-dev",
      serverVersion: "26.1.0",
      storageDriver: "overlay2",
      operatingSystem: "Ubuntu 24.04",
      architecture: "x86_64",
      cpus: 8,
      memoryBytes: 8 * 1024 * 1024 * 1024,
    }),
  978203467: () => JSON.stringify({ Id: container.id, Name: container.name }),
  4209014116: () => (isSeededFixture() ? seededContainers() : [container]),
  54024818: () => (isSeededFixture() ? seededImages() : [image]),
  3981221319: () => (isSeededFixture() ? seededNetworks() : [network]),
  4113921307: () => (isSeededFixture() ? seededVolumes() : [volume]),
  2286805465: () => "load-job",
  795801790: () => commandPlan("Kill web", "docker kill web"),
  3091027417: (kind) =>
    commandPlan(`Prune ${String(kind)}`, `docker ${String(kind)} prune`),
  783154554: () => commandPlan("Remove web", "docker rm web", risk.destructive),
  590018692: () =>
    commandPlan("Remove image", "docker image rm cairn/web:latest"),
  3243822551: () => commandPlan("Remove network", "docker network rm cairn"),
  1592087289: () =>
    commandPlan("Remove volume", "docker volume rm cairn_data", risk.dangerous),
  1735976676: () => "pull-stream",
  3287037137: () => "push-stream",
  597124842: () => "container-new",
  313546242: () => "save-job",
  3553985035: () => [
    {
      name: "nginx",
      description: "Official nginx image",
      stars: 20000,
      official: true,
      automated: false,
    },
  ],
  2954904332: () =>
    dockerStopped({
      clientVersion: "26.1.0",
      serverVersion: "26.1.0",
      apiVersion: "1.45",
    }),
  1987249976: () => dockerStopped(undefined),
  3838558739: lineage,
  3200582322: () => lineage()[0],
  2932089088: lineage,
  1624384586: () => lineage()[0],
  2941853753: () => lineage()[0],
  322133470: () => ({
    path: "/tmp/cairn-logs.jsonl",
    bytesWritten: 128,
    lineCount: 2,
  }),
  3957531718: () => ({
    lines: [
      {
        ts: "2026-06-13T09:00:00Z",
        containerID: container.id,
        containerName: container.name,
        service: container.service,
        stream: "stdout",
        level: "info",
        text: "INFO release validation log line",
      },
    ],
    nextCursor: "",
  }),
  3715102761: () => {
    const streamID = "logs-stream-1";
    if (isSeededFixture()) {
      globalThis.setTimeout(() => {
        Events.Emit("logs:lines", {
          streamID,
          lines: seededLogLines(5000),
        });
      }, 75);
    }
    return streamID;
  },
  3200869941: () => ({ series: [] }),
  4233896820: () =>
    isSeededFixture() ? seededDashboardMetrics() : dashboardMetrics(),
  4269150979: () => ({ series: [] }),
  48603856: () => "stats-stream-1",
  595764834: projectDetail,
  3126173247: projectDetail,
  1261700661: () => (isSeededFixture() ? seededProjects() : [projectSummary()]),
  2130027865: (_projectID, removeVolumes) =>
    commandPlan(
      removeVolumes ? "Down app-db with volumes" : "Down app-db",
      removeVolumes ? "docker compose down --volumes" : "docker compose down",
      removeVolumes ? risk.dangerous : risk.destructive,
    ),
  2512814603: () =>
    commandPlan("Redeploy app-db", "docker compose up -d --force-recreate"),
  2709840416: () => (isSeededFixture() ? seededProjects() : [projectSummary()]),
  1257602498: () => ({ streamID: "install-stream-1" }),
  2594388346: providerStatus,
  1325877761: () => ({ linux_native: providerStatus() }),
  2020513694: () => ({
    id: "linux_native",
    name: "Linux Native",
    kind: "linux_native",
    status: providerStatus(),
  }),
  157753513: () => [
    {
      name: "default",
      current: true,
      dockerHost: "unix:///var/run/docker.sock",
      warning: "",
    },
    {
      name: "remote-prod",
      current: false,
      dockerHost: "tcp://10.0.0.4:2375",
      warning: "unencrypted tcp://",
    },
  ],
  4260401113: () => [
    {
      id: "linux_native",
      name: "Linux Native",
      kind: "linux_native",
      active: true,
      status: providerStatus(),
      healthy: true,
    },
  ],
  1372038617: () =>
    commandPlan("Install Docker Engine on Linux", "sudo apt-get update"),
  1935115277: () =>
    commandPlan(
      "Restart Docker backend",
      "systemctl --user restart docker",
      risk.destructive,
    ),
  3550597673: () => undefined,
  3893040676: () => undefined,
  229778316: () => undefined,
  256096338: () => undefined,
  1386500759: () => undefined,
  1617116029: () => undefined,
  572168877: () => [
    { registry: "docker.io", displayName: "Docker Hub", defaultNamespace: "" },
    { registry: "ghcr.io", displayName: "GitHub Container Registry" },
  ],
  262127296: () => [
    {
      registry: "docker.io",
      username: "cairn-user",
      source: "docker_helper",
      loggedIn: true,
      lastVerifiedAt: "2026-06-13T09:00:00Z",
    },
  ],
  4104214801: () => ({
    registry: "docker.io",
    ok: true,
    username: "cairn-user",
    checkedAt: "2026-06-13T09:00:00Z",
  }),
  2188903172: () => ({
    version: "1.0.0",
    commit: "release-validation",
    buildDate: "2026-06-13T09:00:00Z",
    goVersion: "go1.26.4",
  }),
  689753624: auditEntries,
  2903204757: () => [
    {
      category: "containers",
      command: "docker ps",
      description: "List running containers",
      risk: risk.safe,
      runnable: true,
    },
    {
      category: "cleanup",
      command: "docker system prune",
      description: "Remove unused Docker data",
      risk: risk.dangerous,
      runnable: false,
    },
  ],
  340867397: () => [
    {
      id: 1,
      level: "warn",
      title: "Provider degraded",
      body: "Docker daemon stopped",
      topic: "provider",
      read: false,
      createdAt: "2026-06-13T09:00:00Z",
    },
  ],
  4257326101: () => undefined,
  3314614998: () => ({
    "appearance.theme": "system",
    "security.confirm_destructive": true,
    "registry.credentials_mode": "docker_helper",
    "linux.sudo_mode": "ask",
    "windows.wsl_distro": "Ubuntu",
    "macos.colima_profile": "default",
    "macos.colima_cpu": 2,
    "macos.colima_memory_gb": 4,
    "macos.colima_disk_gb": 60,
    "provider.autostart_backend": true,
    "updates.notify": true,
    "updates.check_interval_hours": 24,
    "metrics.sample_interval_seconds": 2,
    "terminal.default_shell": "sh",
    "backups.directory": "/tmp/cairn-backups",
    "agent.enabled": true,
    "agent.provider": "ollama",
    "agent.endpoint": "http://127.0.0.1:11434",
    "agent.model": "gemma4:12b",
    "agent.max_context_lines": 400,
  }),
  440305753: () => ["/bin/sh"],
  3501313961: () => [],
  830635268: () => terminalSession("backend"),
  2768125935: () => terminalSession("container"),
  288046414: () => terminalSession("host"),
  70372101: () => terminalSession("project"),
  2969046818: () => "updates-apply-job",
  3172433760: () => "updates-check-job",
  1025649146: updates,
  3527487467: () => updates()[0],
  1290489764: (filter) =>
    callListCurrentUpdates(filter as { status?: string[] }),
  1871912704: () => [
    {
      id: 301,
      projectID: "linux_native/app-db",
      service: "app",
      kind: update.serviceImage,
      result: "success",
      startedAt: "2026-06-13T09:05:00Z",
      finishedAt: "2026-06-13T09:06:00Z",
      rollbackStatus: "available",
    },
  ],
  3794215738: updatePlan,
  1485836880: updatePlan,
  2168820661: () => "updates-rollback-job",
};
