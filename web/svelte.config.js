// Adapted from Open WebUI v0.12.5 svelte.config.js: keep the static SPA
// architecture, output to Blux's existing Go static directory, omit upstream
// release polling and backend-specific build steps. See UPSTREAM.md.
import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  kit: {
    adapter: adapter({ pages: 'dist', assets: 'dist', fallback: 'index.html' })
  }
};
