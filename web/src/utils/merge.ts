export function sortedBySeq<T extends { seq: number }>(items: readonly T[]): T[] {
  return [...items].sort((a, b) => a.seq - b.seq)
}

export function mergeBySeq<T extends { seq: number }>(
  current: T[],
  incoming: T[],
  maxItems?: number,
): T[] {
  if (incoming.length === 0) return current
  const merged = new Map<number, T>()
  for (const item of current) merged.set(item.seq, item)
  for (const item of incoming) merged.set(item.seq, item)
  let out = [...merged.values()].sort((a, b) => a.seq - b.seq)
  if (maxItems && out.length > maxItems) out = out.slice(out.length - maxItems)
  return out
}
