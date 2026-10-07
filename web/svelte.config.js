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
		},

		/**
		 * The build's own version string, which SvelteKit puts in
		 * `_app/version.json` and the service worker reads to decide whether a new
		 * version has been deployed.
		 *
		 * Its default is `Date.now()`, and that made every build of this frontend
		 * produce different bytes: the millisecond lands in version.json, in
		 * index.html and in service-worker.js, which changes the content hash of
		 * every chunk that references them, which changes those chunks' filenames.
		 * So two builds of the same commit had different file names in them.
		 *
		 * That is not an aesthetic problem, and it had two costs. The Dockerfile
		 * said `-trimpath` and an empty buildid "keep the output reproducible", and
		 * that sentence had been false for a year — the flags did their job and the
		 * frontend embedded in the binary carried a millisecond. And the Dockerfile
		 * builds one binary into two images, verdande and urd, where the claim is
		 * that they are the same program rather than a fork; with a timestamp in the
		 * build, that claim could not be checked, because no two builds ever
		 * matched. CI checks it now.
		 *
		 * Measured by building twice and diffing the tree, before and after.
		 *
		 * `dev` when nothing sets it, so a local build is stable too. The cost is
		 * that a local `npm run build` no longer looks like a new version to the
		 * service worker — which only matters on a deployed instance, where the
		 * release workflow passes the real tag.
		 */
		version: {
			name: process.env.VERDANDE_VERSION ?? 'dev'
		}
	}
};
