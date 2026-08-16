// The whole app is client-rendered: every page needs a signed-in user, so there is
// nothing meaningful to render on a server that has no session. `ssr = false` also
// means the static adapter emits one index.html shell, which is exactly what the Go
// binary serves for any unrecognised route.
export const ssr = false;
export const prerender = false;
