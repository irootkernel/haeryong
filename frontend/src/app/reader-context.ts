// Hash-only absent-state entries retain the live document context. A different
// document establishes its own context; a stamped entry always retains its own.
export function readerContext(previousDocument: string, nextDocument: string, liveContext: string, stamped: unknown, create: () => string): string {
  if (typeof stamped === "string") return stamped;
  return nextDocument === previousDocument ? liveContext : create();
}
