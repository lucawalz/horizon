export function errorFor(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error(String(cause))
}
