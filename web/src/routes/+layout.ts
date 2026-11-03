// Same client-only static deployment model as Open WebUI. All durable state is
// owned by the existing Go API, not a SvelteKit/Node server or browser storage.
export const ssr = false;
