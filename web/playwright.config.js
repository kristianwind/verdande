import { defineConfig, devices } from '@playwright/test';
import { rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

/**
 * End-to-end smoke tests.
 *
 * These drive the *whole* application — the Go binary with the frontend embedded
 * in it — rather than the dev server. That is deliberate: the two bugs that
 * survived longest in this project were a route written outside the routes tree
 * and a manifest promising files that were never generated. Neither is visible to
 * a Go test, and neither is visible to a Vite build either. Only something that
 * opens the real binary and clicks the real links finds them.
 *
 * `go run` rather than a prebuilt binary: the build cache makes the second run
 * fast, and there is no separate build step to forget.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const dataDir = join(here, '.playwright-data');

// A clean instance every run: the first test creates the first account, which only
// works on an empty database.
//
// Only in the main process. Playwright re-imports this file in every worker, and a
// wipe there deletes the database out from under the running server — which does
// not fail loudly. Connections already open keep working against the unlinked
// inode, so most requests still succeed and only the ones that need a *new*
// connection fail, with SQLITE_CANTOPEN. It looks exactly like a flaky auth bug.
if (process.env.TEST_WORKER_INDEX === undefined) {
	rmSync(dataDir, { recursive: true, force: true });
}

const PORT = 8097;
const baseURL = `http://127.0.0.1:${PORT}`;

export default defineConfig({
	testDir: './e2e',
	// One worker, and not parallel: there is one server, one database and one
	// account. Parallelism here would only test whether the tests race.
	workers: 1,
	fullyParallel: false,
	forbidOnly: !!process.env.CI,
	retries: 0,
	reporter: process.env.CI ? [['github'], ['list']] : 'list',

	use: {
		baseURL,
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure',
		// Pinned, because the app reads both. The sign-up form sends
		// `navigator.language` as the account's locale — which decides whether
		// "i morgen" or "tomorrow" parses — and every due date is resolved in the
		// account's zone. On a CI runner in UTC with an en-US browser, an
		// unpinned suite tests a different application than the one people use.
		locale: 'da-DK',
		timezoneId: 'Europe/Copenhagen'
	},

	projects: [
		{ name: 'setup', testMatch: /.*\.setup\.js/ },
		{
			name: 'chromium',
			use: { ...devices['Desktop Chrome'], storageState: 'e2e/.auth/user.json' },
			dependencies: ['setup']
		},
		/**
		 * A short WebKit pass over the forms, and nothing else.
		 *
		 * The rest of this suite is Chromium only on purpose: a smoke test exists to
		 * catch a build that does not work, and running it in three engines finds the
		 * same bug three times. Focus is the exception, and it cost a real report to
		 * learn it. Safari ignores the `autofocus` attribute on anything inserted
		 * after the page has loaded — so every inline form in this app opened with no
		 * cursor in it, and what you typed went nowhere. It does not look like a bug;
		 * it looks like the feature does not work. "Sections have no function,
		 * because you cannot create more than one" was this.
		 *
		 * One test, not four. The engines share this server and this database, so a
		 * second pass over tests that create projects by name collides with what the
		 * first pass already made — which fails as a name that is suddenly ambiguous
		 * rather than as anything about WebKit. The sections test is the one that
		 * earns the second engine, because it is the one the focus bug broke.
		 *
		 * WebKit here is the same engine Safari is, which is the whole point.
		 */
		{
			name: 'webkit-forms',
			grep: /@forms/,
			use: { ...devices['Desktop Safari'], storageState: 'e2e/.auth/user.json' },
			dependencies: ['setup']
		}
	],

	webServer: {
		// The whole chain, every run.
		//
		// The frontend is embedded in the binary at compile time, from a copy under
		// cmd/verdande/webbuild — so a suite that merely started the server would
		// test whatever frontend was copied there last, which during development is
		// nearly always the wrong one. Testing a stale build is worse than not
		// testing: it reports green on code that is not running.
		command: [
			'npm run build',
			'rm -rf ../cmd/verdande/webbuild',
			'cp -r build ../cmd/verdande/webbuild',
			'cd .. && go run -tags embedweb ./cmd/verdande'
		].join(' && '),
		cwd: here,
		url: `${baseURL}/healthz`,
		reuseExistingServer: false,
		// The first run compiles the binary and builds nothing else; a cold Go
		// build on a CI runner is comfortably under two minutes.
		timeout: 180_000,
		stdout: 'pipe',
		stderr: 'pipe',
		env: {
			VERDANDE_ADDR: `:${PORT}`,
			VERDANDE_BASE_URL: baseURL,
			VERDANDE_DATA_DIR: dataDir,
			// Off: an update check would reach out to GitHub from a test run.
			VERDANDE_UPDATE_CHECK: 'false'
		}
	}
});
