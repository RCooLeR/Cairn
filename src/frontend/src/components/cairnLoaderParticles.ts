export type ParticlePoint = {
  x: number;
  y: number;
};

export const PARTICLE_LINK_DISTANCE_SQUARED = 11_500;
export const MAX_PARTICLE_LINK_CANDIDATES = 24;

const PARTICLE_LINK_CELL_SIZE = Math.sqrt(PARTICLE_LINK_DISTANCE_SQUARED);
const NEIGHBOR_CELL_OFFSETS = [
  [0, 0],
  [-1, 0],
  [1, 0],
  [0, -1],
  [0, 1],
  [-1, -1],
  [1, -1],
  [-1, 1],
  [1, 1],
] as const;

function cellKey(x: number, y: number) {
  return `${x}:${y}`;
}

/**
 * Visits visually useful particle links without comparing every particle pair.
 *
 * The grid limits candidates to cells that can contain a point within the link
 * radius. The per-particle candidate budget also keeps dense clusters linear
 * in particle count; dropping an occasional decorative link is preferable to
 * allowing a startup animation to monopolize the UI thread.
 */
export function visitParticleLinks<T extends ParticlePoint>(
  particles: readonly T[],
  visit: (a: T, b: T, distanceSquared: number) => void,
) {
  const cells = new Map<string, number[]>();
  let comparisons = 0;

  for (let index = 0; index < particles.length; index += 1) {
    const particle = particles[index];
    const cellX = Math.floor(particle.x / PARTICLE_LINK_CELL_SIZE);
    const cellY = Math.floor(particle.y / PARTICLE_LINK_CELL_SIZE);
    let candidates = 0;

    for (const [offsetX, offsetY] of NEIGHBOR_CELL_OFFSETS) {
      const bucket = cells.get(cellKey(cellX + offsetX, cellY + offsetY));
      if (!bucket) continue;

      for (
        let bucketIndex = bucket.length - 1;
        bucketIndex >= 0;
        bucketIndex -= 1
      ) {
        if (candidates >= MAX_PARTICLE_LINK_CANDIDATES) break;

        const other = particles[bucket[bucketIndex]];
        const dx = particle.x - other.x;
        const dy = particle.y - other.y;
        const distanceSquared = dx * dx + dy * dy;
        candidates += 1;
        comparisons += 1;

        if (distanceSquared < PARTICLE_LINK_DISTANCE_SQUARED) {
          visit(other, particle, distanceSquared);
        }
      }

      if (candidates >= MAX_PARTICLE_LINK_CANDIDATES) break;
    }

    const key = cellKey(cellX, cellY);
    const bucket = cells.get(key);
    if (bucket) {
      bucket.push(index);
    } else {
      cells.set(key, [index]);
    }
  }

  return comparisons;
}
