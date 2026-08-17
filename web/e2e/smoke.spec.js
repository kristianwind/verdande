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

		// Wait for the navigation before looking at the page. Without this the
		// assertion can be satisfied by the heading of the page you just left —
		// which is not a hypothetical: a change that removed a project page's
		// heading entirely passed here on a fast machine and failed on CI.
		await page.waitForURL(`**${href}`);

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

	await expect(page.getByRole('heading', { name: 'Ferie' })).toBeVisible();

	// The heading is the rename affordance: click it, and it becomes a field.
	await page.getByRole('button', { name: 'Ferie' }).click();
	const title = page.getByLabel('Projektets navn');
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
 * A group can be made, folded, filled and deleted without taking its projects
 * with it.
 *
 * The fold is the part worth driving in a browser: it is stored on the account
 * rather than in localStorage, so it is a round trip, and "the arrow turned" is
 * not the same claim as "it came back folded after a reload".
 */
test('en projektgruppe kan foldes, fyldes og slettes uden at tage projekterne med', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });

	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Regnskab');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Regnskab' })).toBeVisible();

	await sidebar.getByRole('button', { name: 'Ny gruppe' }).click();
	await sidebar.getByLabel('Gruppens navn').fill('Arbejde');
	await sidebar.getByLabel('Gruppens navn').press('Enter');

	const heading = sidebar.getByRole('button', { name: 'Arbejde' });
	await expect(heading).toBeVisible();
	await expect(sidebar.getByText('Træk et projekt herop')).toBeVisible();

	// Dragged in, then dragged back out onto "Projekter" — which is the reason
	// that heading is a drop target at all: with every project filed away there
	// would otherwise be no loose row left to aim at.
	await sidebar.getByRole('link', { name: 'Regnskab' }).dragTo(heading);
	await expect(sidebar.getByText('Træk et projekt herop')).toBeHidden();

	await sidebar
		.getByRole('link', { name: 'Regnskab' })
		.dragTo(sidebar.getByRole('heading', { name: 'Projekter' }));
	await expect(sidebar.getByText('Træk et projekt herop')).toBeVisible();

	// Folded, reloaded, still folded. localStorage would have passed the first
	// half of that and failed the second on another machine.
	await heading.click();
	await expect(heading).toHaveAttribute('aria-expanded', 'false');
	await page.reload();
	await expect(sidebar.getByRole('button', { name: 'Arbejde' })).toHaveAttribute(
		'aria-expanded',
		'false'
	);
	await sidebar.getByRole('button', { name: 'Arbejde' }).click();

	// Renaming writes one row rather than one per project, which is the reason a
	// group is a table and not a string repeated on every project.
	await sidebar.getByRole('button', { name: 'Omdøb' }).click();
	const name = sidebar.getByLabel('Gruppens navn');
	await name.fill('Kontoret');
	await name.press('Enter');
	await expect(sidebar.getByRole('button', { name: 'Kontoret' })).toBeVisible();

	// Deleting the heading must not take the project filed under it. It is
	// ungrouped here, which is the case that would look identical either way if
	// the assertion were only "the group is gone".
	page.once('dialog', (dialog) => dialog.accept());
	await sidebar.getByRole('button', { name: 'Slet' }).click();
	await expect(sidebar.getByRole('button', { name: 'Kontoret' })).toBeHidden();
	await expect(sidebar.getByRole('link', { name: 'Regnskab' })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * A task can be dragged onto another day, and onto another project.
 *
 * Both are one gesture standing in for a form, and both cross a component
 * boundary: the row is dragged out of Kommende and dropped on the sidebar, which
 * is a different part of the tree entirely. What decides whether a target will
 * take it is the drag's MIME type, and that is only readable while the drag is in
 * the air — so this is the one thing here that cannot be checked any other way.
 */
test('en opgave kan trækkes til en anden dag og videre til et projekt', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	await box.fill('hent pakken i dag');
	await box.press('Enter');
	await expect(page.getByText('hent pakken', { exact: true })).toBeVisible();

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Ærinder');
	await sidebar.getByLabel('Projektnavn').press('Enter');

	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Liste', exact: true }).click();

	const day = (name) =>
		page.locator('section').filter({ has: page.getByRole('heading', { name, exact: true }) });
	const row = page.locator('[draggable="true"]').filter({ hasText: 'hent pakken' });

	await expect(day('I dag').getByText('hent pakken')).toBeVisible();
	await row.dragTo(day('I morgen'));
	await expect(day('I morgen').getByText('hent pakken')).toBeVisible();
	await expect(day('I dag').getByText('hent pakken')).toBeHidden();

	// And onto a project, which is a move rather than a field: the task has to
	// find a place among that project's tasks, and leave its old section behind.
	await row.dragTo(sidebar.getByRole('link', { name: 'Ærinder' }));
	await sidebar.getByRole('link', { name: 'Ærinder' }).click();
	await expect(page.getByRole('heading', { name: 'Ærinder' })).toBeVisible();
	await expect(page.getByText('hent pakken', { exact: true })).toBeVisible();

	// And the same task in the month grid, where a chip is dragged from one cell
	// to another. The date is read off the grid rather than worked out here: the
	// browser is pinned to Europe/Copenhagen and this process is not, and for two
	// hours of every day they disagree about what today is.
	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Kalender' }).click();

	const chip = page.locator('.chip').filter({ hasText: 'hent pakken' });
	await expect(chip).toBeVisible();

	const from = await page.locator('.day').filter({ has: chip }).getAttribute('data-date');
	const to = new Date(new Date(`${from}T12:00:00Z`).getTime() + 86400000)
		.toISOString()
		.slice(0, 10);

	await chip.dragTo(page.locator(`[data-date="${to}"]`));
	await expect(page.locator(`[data-date="${to}"]`).getByText('hent pakken')).toBeVisible();
	await expect(page.locator(`[data-date="${from}"]`).getByText('hent pakken')).toBeHidden();

	expect(trouble).toEqual([]);
});

/**
 * An invite link creates the account it was sent to.
 *
 * The server has emailed this URL since invites were built and nothing rendered
 * for it: the SPA fallback served the shell, the shell found no session, and the
 * recipient got a plain sign-in form for an account that did not exist yet. A 200
 * all the way down — the same shape as `/mcp` and `/.well-known/`, and invisible
 * to every test that does not open the link.
 *
 * A second browser context, because the invitee is a different person: inheriting
 * the signed-in session would test the one case that never happens.
 */
test('et invitationslink opretter kontoen og giver adgang til projektet', async ({
	browser,
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Fælleshuset');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Fælleshuset' })).toBeVisible();

	await page.getByRole('button', { name: 'Del' }).click();
	await page.getByLabel('Inviter via e-mail').fill('nabo@example.dk');
	await page.getByRole('button', { name: 'Inviter' }).click();

	// With no mail server the link is shown rather than swallowed, which is also
	// the only way this test can get hold of it.
	const link = await page.locator('.link-out code').textContent();
	expect(link).toContain('/invite?token=');

	// Explicitly empty, because a context made through the `browser` fixture picks
	// up the project's storageState — and an invitee who is already signed in as
	// the person who sent the invite is the one case that never happens.
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const invitee = await context.newPage();
	const inviteeTrouble = watchForTrouble(invitee);
	await invitee.goto(link);

	await expect(invitee.getByText('Du er inviteret')).toBeVisible();
	await invitee.getByLabel('Navn').fill('Nabo');
	await invitee.getByLabel(/Adgangskode/).fill('et langt kodeord til test');
	await invitee.getByRole('button', { name: 'Opret konto' }).click();

	// Signed in, with the project they were invited to.
	await expect(invitee.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();
	await expect(
		invitee.getByRole('navigation', { name: 'Hovedmenu' }).getByRole('link', { name: /Fælleshuset/ })
	).toBeVisible();

	// One failure here is not trouble: the page loads before the account exists, so
	// the shell's first `me()` is answered 401. That 401 is how the signed-out
	// screen appears at all.
	expect(inviteeTrouble.filter((t) => t !== 'GET /api/v1/auth/me → 401')).toEqual([]);
	await context.close();

	// Now that there is somebody to delegate to, the inviter can — and the view
	// for it appears. It is hidden on an instance with one person, where it could
	// never have anything in it.
	await page.reload();
	await expect(sidebar.getByRole('link', { name: 'Venter på andre' })).toBeVisible();

	// A project the inviter owns stays under "Projekter" after sharing it. It used
	// to move to "Delt med mig" the moment somebody accepted, and lose its place in
	// the order on the way.
	await expect(
		page.locator('.group').filter({ hasText: 'Projekter' }).getByText('Fælleshuset')
	).toBeVisible();

	await page.getByLabel('Ny opgave').fill('males inden fredag #Fælleshuset');
	await page.getByLabel('Ny opgave').press('Enter');
	await page.getByText('males inden fredag', { exact: true }).click();

	const drawer = page.getByRole('complementary', { name: 'Opgave' });
	await drawer.getByLabel('Ansvarlig').selectOption({ label: 'Nabo' });
	await page.keyboard.press('Escape');

	await sidebar.getByRole('link', { name: 'Venter på andre' }).click();
	await expect(page.getByRole('heading', { name: 'Venter på andre' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Nabo' })).toBeVisible();
	await expect(page.getByText('males inden fredag')).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * The reset link renders its own form.
 *
 * The token is nonsense, so this stops at the form — which is the part that did
 * not exist. "Glemt adgangskode?" has always sent a link to this address and the
 * API has always accepted the token; what the link opened was the sign-in screen,
 * asking for the password the person had just said they had forgotten.
 */
test('nulstillingslinket viser et felt til den nye adgangskode', async ({ browser }) => {
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const page = await context.newPage();

	await page.goto('/reset?token=ugyldigt');
	await expect(page.getByText('Vælg en ny adgangskode')).toBeVisible();

	await page.getByLabel(/adgangskode/i).fill('et langt kodeord til test');
	await page.getByRole('button', { name: 'Gem adgangskoden' }).click();

	// And it says so rather than pretending: the token is not a real one.
	await expect(page.getByRole('alert')).toBeVisible();
	await context.close();
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
