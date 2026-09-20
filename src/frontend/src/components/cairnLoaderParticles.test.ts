import { describe, expect, it, vi } from "vitest";

import {
  MAX_PARTICLE_LINK_CANDIDATES,
  PARTICLE_LINK_DISTANCE_SQUARED,
  visitParticleLinks,
} from "./cairnLoaderParticles";

describe("visitParticleLinks", () => {
  it("visits each nearby pair exactly once", () => {
    const particles = [
      { id: "origin", x: 0, y: 0 },
      { id: "east", x: 50, y: 0 },
      { id: "south", x: 0, y: 70 },
      { id: "far", x: 500, y: 500 },
    ];
    const links: string[] = [];

    visitParticleLinks(particles, (a, b) => links.push(`${a.id}:${b.id}`));

    expect(links.sort()).toEqual(["east:south", "origin:east", "origin:south"]);
  });

  it("checks only adjacent grid cells", () => {
    const visit = vi.fn();
    const particles = [
      { x: 0, y: 0 },
      { x: Math.sqrt(PARTICLE_LINK_DISTANCE_SQUARED) * 3, y: 0 },
    ];

    expect(visitParticleLinks(particles, visit)).toBe(0);
    expect(visit).not.toHaveBeenCalled();
  });

  it("bounds dense-cluster work by a fixed candidate budget", () => {
    const particleCount = 1_000;
    const particles = Array.from({ length: particleCount }, () => ({
      x: 10,
      y: 10,
    }));
    const visit = vi.fn();

    const comparisons = visitParticleLinks(particles, visit);

    expect(comparisons).toBeLessThanOrEqual(
      particleCount * MAX_PARTICLE_LINK_CANDIDATES,
    );
    expect(comparisons).toBeLessThan((particleCount * (particleCount - 1)) / 2);
    expect(visit).toHaveBeenCalledTimes(comparisons);
  });
});
