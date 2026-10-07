/**
 * Collects anything that went wrong quietly: a thrown exception, a console error,
 * or an API call that came back a failure.
 *
 * Failed responses are recorded with their URL and status. "A console error
 * happened" is not an actionable test failure — "PATCH /api/v1/tasks/x returned
 * 500" is.
 *
 * Udskilt fra smoke.spec.js, så udgave-suiten kan bruge den samme — og den
 * fortjente flytningen med det samme: lukker man en rute, fladen stadig kalder,
 * står der en 404 på en side, der ser helt rigtig ud, og det her er det eneste,
 * der ser den.
 */
export function watchForTrouble(page) {
	const trouble = [];
	page.on('console', (message) => {
		// The browser logs its own line for every failed fetch. It carries no URL,
		// so it is noise next to the response listener below.
		if (message.type() === 'error' && !message.text().includes('Failed to load resource')) {
			trouble.push(message.text());
		}
	});
	page.on('pageerror', (error) => trouble.push(String(error)));
	page.on('response', (response) => {
		const url = response.url();
		if (!url.includes('/api/') || response.ok()) return;

		// A 401 from /auth/me is the app asking "am I signed in?" and being told no,
		// which is the correct answer on the sign-in page and after signing out.
		// Every other non-ok response is worth failing over — that is what this
		// watcher is for.
		const path = new URL(url).pathname;
		if (path.endsWith('/auth/me') && response.status() === 401) return;

		trouble.push(`${response.request().method()} ${path} → ${response.status()}`);
	});
	return trouble;
}
