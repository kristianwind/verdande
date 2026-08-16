import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
export default {
	kit: {
		// A single-page app: the Go binary embeds `build/` and serves index.html for
		// any route it does not recognise, so the client router owns navigation.
		// Prerendering is off because every page needs a signed-in user.
		adapter: adapter({
			pages: 'build',
			assets: 'build',
			fallback: 'index.html',
			precompress: false
		}),
		alias: {
			$lib: 'src/lib'
		}
	}
};
