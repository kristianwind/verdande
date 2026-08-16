import { sveltekit } from '@sveltejs/kit/vite';

export default {
	plugins: [sveltekit()],
	server: {
		port: 5173,
		// In development the app runs on 5173 and the API on 8080. Proxying rather
		// than pointing fetch at another origin keeps the session cookie
		// first-party, which is what production looks like — otherwise every
		// cookie and CSRF behaviour would differ between dev and the real thing.
		proxy: {
			'/api': {
				target: 'http://localhost:8080',
				changeOrigin: false,
				ws: true
			}
		}
	}
};
