import { test, expect } from '@playwright/test';

/**
 * The four flows a broken build must not survive: add a task, tick it off, reach
 * every page in the sidebar, and open a task.
 *
 * Every test asserts on *rendered* content rather than on a URL. A route that no
 * longer exists still answers 200 here — the Go binary serves the SPA shell for
 * anything it does not recognise — so `expect(page).toHaveURL()` would pass on
 * precisely the bug this suite exists to catch.
 */

/**
 * Collects anything that went wrong quietly: a thrown exception, a console error,
 * or an API call that came back a failure.
 *
 * Failed responses are recorded with their URL and status. "A console error
 * happened" is not an actionable test failure — "PATCH /api/v1/tasks/x returned
 * 500" is.
 */
function watchForTrouble(page) {
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
		if (url.includes('/api/') && !response.ok()) {
			trouble.push(`${response.request().method()} ${new URL(url).pathname} → ${response.status()}`);
		}
	});
	return trouble;
}

test('hurtig tilføjelse opretter en opgave, og den kan lukkes', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	// "i dag" rather than "i morgen": this is the Today view, and a task parsed
	// onto tomorrow is correctly absent from it — which would look like the quick
	// add had failed.
	await box.fill('køb kaffe i dag p1');
	await box.press('Enter');

	// The server parses the line, so this asserts the whole round trip: "i dag"
	// and "p1" are gone from the title because they became a date and a priority.
	const task = page.getByText('køb kaffe', { exact: true });
	await expect(task).toBeVisible();

	await page.getByRole('button', { name: 'Markér som færdig' }).first().click();
	await expect(task).toBeHidden();

	expect(trouble).toEqual([]);
});

test('hvert link i sidebjælken fører et sted hen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });

	// Wait for one link before counting them. `all()` is a snapshot with no
	// auto-waiting — it returns whatever matches at that instant — so counting
	// straight after goto() measures how fast the machine is. It passed on a
	// laptop and found zero links on a CI runner.
	await expect(sidebar.getByRole('link').first()).toBeVisible();

	const hrefs = await sidebar
		.getByRole('link')
		.evaluateAll((links) => links.map((a) => a.getAttribute('href')));
	expect(hrefs.length).toBeGreaterThanOrEqual(4); // I dag, Kommende, Indbakke, Indstillinger

	// Re-resolved by href on each pass rather than held as handles: navigating
	// re-renders the sidebar, and a handle from before the click is a handle to
	// an element that no longer exists.
	for (const href of hrefs) {
		await sidebar.locator(`a[href="${href}"]`).first().click();

		// Something with a heading rendered. A route written outside the routes
		// tree lands on the SPA shell and renders nothing — which is exactly how
		// the label route broke, and it answered 200 the whole time.
		await expect(
			page.locator('main h1, main h2').first(),
			`${href} renderede ingenting`
		).toBeVisible({ timeout: 5000 });
	}

	expect(trouble).toEqual([]);
});

test('en opgave kan åbnes og redigeres', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	// Dated, so it lands in this view: an undated task goes to the Inbox and is
	// correctly absent here.
	await box.fill('skriv rapport i dag');
	await box.press('Enter');

	await page.getByText('skriv rapport', { exact: true }).click();

	const drawer = page.getByRole('complementary', { name: 'Opgave' });
	await expect(drawer).toBeVisible();

	// A sub-task, a comment and a description at once — which is also the shape
	// that used to fail: three concurrent writes met SQLite's deferred BEGIN and
	// one of them came back a 500.
	await drawer.getByLabel('Beskrivelse').fill('Inden fredag.');
	await drawer.getByLabel('Ny undertask').fill('lav dispositionen');
	await drawer.getByLabel('Ny undertask').press('Enter');
	await drawer.getByLabel('Ny kommentar').fill('Anders har materialet.');
	await drawer.getByRole('button', { name: 'Skriv' }).click();

	await expect(drawer.getByText('lav dispositionen')).toBeVisible();
	await expect(drawer.getByText('Anders har materialet.')).toBeVisible();

	// Nothing rolled back: a toast here means the server refused a write.
	await expect(page.locator('.toast')).toHaveCount(0);

	await page.keyboard.press('Escape');
	await expect(drawer).toBeHidden();

	// The description reached the row behind the drawer, so it was really saved.
	await expect(page.getByText('Inden fredag.')).toBeVisible();

	expect(trouble).toEqual([]);
});

test('hver fane under indstillinger renderer', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger');

	const tabs = page.getByRole('navigation', { name: 'Indstillinger' });
	await expect(tabs).toBeVisible();

	for (const label of [
		'Konto',
		'Notifikationer',
		'Integrationer',
		'AI',
		'API-tokens',
		'Data og skabeloner'
	]) {
		await tabs.getByRole('link', { name: label, exact: true }).click();
		await expect(
			page.locator('section.panel').first(),
			`fanen ${label} renderede ingen sektion`
		).toBeVisible({ timeout: 5000 });
	}

	expect(trouble).toEqual([]);
});

/**
 * The PWA's own files. Both of the bugs found last in this project were of this
 * kind — a manifest promising icons that were never generated — and they are
 * invisible to every other test in the repository.
 */
test('manifestet og service workeren findes, og ikonerne findes også', async ({ request }) => {
	const manifestResponse = await request.get('/manifest.webmanifest');
	expect(manifestResponse.ok()).toBeTruthy();

	const manifest = await manifestResponse.json();
	for (const icon of manifest.icons ?? []) {
		const iconResponse = await request.get(icon.src);
		expect(iconResponse.ok(), `${icon.src} findes ikke`).toBeTruthy();
	}

	// The service worker is what Web Push needs; without it a subscription cannot
	// be created at all.
	const worker = await request.get('/sw.js');
	expect(worker.ok()).toBeTruthy();
	expect(await worker.text()).toContain("addEventListener('push'");
});

/**
 * A project can be renamed, deleted and brought back.
 *
 * The round trip matters more than the three steps: deleting used to be a
 * one-way door in the interface — the API could restore, but only from an id
 * that had already disappeared from the screen.
 */
test('et projekt kan omdøbes, slettes og hentes tilbage', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Ferie');
	await sidebar.getByLabel('Projektnavn').press('Enter');

	const title = page.getByLabel('Projektets navn');
	await expect(title).toHaveValue('Ferie');

	await title.fill('Sommerferie');
	await title.press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Sommerferie' })).toBeVisible();

	page.once('dialog', (dialog) => dialog.accept());
	await page.getByRole('button', { name: 'Slet projektet' }).click();

	await expect(sidebar.getByRole('link', { name: 'Sommerferie' })).toBeHidden();

	// And it is reachable again, which is the half that did not exist.
	await page.goto('/indstillinger/data');
	const trash = page.locator('section.panel').filter({ hasText: 'Papirkurv' });
	await expect(trash.getByText('Sommerferie')).toBeVisible();

	await trash.getByRole('button', { name: 'Hent tilbage' }).click();
	await expect(sidebar.getByRole('link', { name: 'Sommerferie' })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * The MCP connector address must not fall through to the app shell.
 *
 * It did: `/mcp` was not a route, so the SPA fallback answered 200 with a page
 * of HTML. A connector pointed at it reported a successful connection and then
 * failed to parse the response — which looks like a broken client rather than a
 * missing route. A 401 in JSON is the honest answer.
 */
test('MCP-adressen svarer JSON, ikke app-skallen', async ({ request }) => {
	const response = await request.post('/mcp', {
		data: { jsonrpc: '2.0', id: 1, method: 'initialize' }
	});

	expect(response.status(), 'uden nøgle skal den afvise, ikke servere siden').toBe(401);
	expect(response.headers()['content-type']).toContain('application/json');
	expect((await response.json()).code).toBe('unauthorized');
});
