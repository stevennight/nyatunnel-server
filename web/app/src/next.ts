/**
 * Login hand-off: login-gated tunnels send visitors to /login?next=/tunnel-login?... . Only
 * same-origin paths are followed; anything else (absolute URLs, protocol-relative "//host",
 * backslash tricks) is ignored.
 */
export function safeNext(raw: string | null | undefined): string | null {
  if (!raw) return null
  if (!raw.startsWith('/') || raw.startsWith('//')) return null
  // Backslashes ("/\evil.example" is "//evil.example" to browsers) and control characters
  // (tabs / newlines are stripped by URL parsers and could form "//").
  if (/[\u0000-\u001f\\]/.test(raw)) return null
  return raw
}

/** Server routes outside the single-page app; they need a full page load. */
export function isServerRoute(path: string): boolean {
  return path === '/tunnel-login' || path.startsWith('/tunnel-login?') || path.startsWith('/tunnel-login/')
}

/** The `next` parameter of the current location, if safe. */
export function nextFromSearch(search: string): string | null {
  return safeNext(new URLSearchParams(search).get('next'))
}

/** Indirection so tests can observe full-page navigations (jsdom cannot navigate). */
export const browser = {
  assign(url: string) {
    window.location.assign(url)
  },
}
