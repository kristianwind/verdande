import { test, expect } from '@playwright/test';
import { USER } from './user.js';

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
		// Only an administrator sees these two, and the account these tests run as
		// is the first account, which is one.
		'Brugere',
		'Historik',
		'Fejl',
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

	// Safari never reads the manifest's icons; it looks for this by name, and
	// without it an installed PWA on iOS gets a screenshot of the page as its
	// home-screen icon.
	const appleIcon = await request.get('/apple-touch-icon.png');
	expect(appleIcon.ok(), 'apple-touch-icon.png findes ikke').toBeTruthy();

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

	// The name is a link to the group's own page; the chevron beside it is a
	// separate button that folds. Two intentions, two targets — the single one they
	// used to share could only ever serve the smaller.
	//
	// `exact` on the link, because a group's name is a user-chosen word that can
	// turn up inside another control's name, and Playwright matches an accessible
	// name by substring unless told otherwise.
	const heading = sidebar.getByRole('link', { name: 'Arbejde', exact: true });
	const fold = sidebar.getByRole('button', { name: /^Fold Arbejde/ });
	await expect(heading).toBeVisible();
	await expect(sidebar.getByText('Træk et projekt herop')).toBeVisible();

	// The words in the heading must not sit on top of each other.
	//
	// Everything else here clicks by accessible name, which is exactly why this
	// needs saying out loud: "Omdøb" and "Slet" once got the 20px square meant for
	// the "+" icon beside "Projekter", overflowed it, and were drawn overlapping.
	// Both buttons still worked, so every assertion in this file passed.
	const boxes = await sidebar.locator('.folder-head').evaluate((el) =>
		// Links as well as buttons: the group's name is a link to its page now, and
		// a measurement that skipped it would miss the widest thing in the row.
		[...el.querySelectorAll('button, a')].map((b) => {
			const r = b.getBoundingClientRect();
			return { text: b.textContent.trim(), left: r.left, right: r.right };
		})
	);
	for (let i = 1; i < boxes.length; i++) {
		expect(
			boxes[i].left,
			`"${boxes[i - 1].text}" og "${boxes[i].text}" overlapper i gruppehovedet`
		).toBeGreaterThanOrEqual(boxes[i - 1].right);
	}

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
	await fold.click();
	await expect(fold).toHaveAttribute('aria-expanded', 'false');
	await page.reload();
	await expect(sidebar.getByRole('button', { name: /^Fold Arbejde/ })).toHaveAttribute(
		'aria-expanded',
		'false'
	);
	await sidebar.getByRole('button', { name: /^Fold Arbejde/ }).click();

	// Colour, which is chosen while renaming rather than from the row. Both are
	// edits to the same thing, and a heading carrying four controls crowded the
	// name it exists to show.
	await sidebar.getByRole('button', { name: 'Omdøb' }).click();
	await sidebar.getByRole('button', { name: 'Petrol' }).click();
	// Still open: picking a colour must not close the form under the pointer, which
	// is what a plain blur-to-cancel does in the two browsers that do not focus a
	// button on click.
	await expect(sidebar.getByLabel('Gruppens navn')).toBeVisible();
	await sidebar.getByLabel('Gruppens navn').press('Enter');

	const painted = await sidebar
		.locator('.folder-head .group-dot')
		.evaluate((el) => getComputedStyle(el).backgroundColor);
	expect(painted, 'gruppens prik fik ikke sin farve').not.toBe('rgba(0, 0, 0, 0)');

	// The heading itself is *not* indented — only what is filed under it. A heading
	// that starts further in than the rows above it says the heading is inside
	// something, which it is not.
	const headLeft = await sidebar
		.locator('.folder-head')
		.evaluate((el) => el.getBoundingClientRect().left + parseFloat(getComputedStyle(el).paddingLeft));
	const looseLeft = await sidebar
		.locator('.views a')
		.first()
		.evaluate((el) => el.getBoundingClientRect().left + parseFloat(getComputedStyle(el).paddingLeft));
	expect(headLeft, 'gruppehovedet er rykket ind').toBeLessThanOrEqual(looseLeft);

	// A project inside a group starts further in than a row outside one. Measured
	// rather than asserted on a class, because indentation is a fact about where
	// the row's contents land — and measured at the *content* edge, not the box:
	// the indent is padding, so the element's own left edge does not move.
	await sidebar.getByRole('link', { name: 'Regnskab' }).dragTo(heading);
	const contentLeft = (el) =>
		el.getBoundingClientRect().left + parseFloat(getComputedStyle(el).paddingLeft);
	const [inside, outside] = await Promise.all([
		sidebar.locator('.folder > a').first().evaluate(contentLeft),
		sidebar.locator('.views a').first().evaluate(contentLeft)
	]);
	expect(inside, 'et projekt i en gruppe er ikke rykket ind').toBeGreaterThan(outside);

	await sidebar
		.getByRole('link', { name: 'Regnskab' })
		.dragTo(sidebar.getByRole('heading', { name: 'Projekter' }));

	// Renaming writes one row rather than one per project, which is the reason a
	// group is a table and not a string repeated on every project.
	await sidebar.getByRole('button', { name: 'Omdøb' }).click();
	const name = sidebar.getByLabel('Gruppens navn');
	await name.fill('Kontoret');
	await name.press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Kontoret', exact: true })).toBeVisible();

	// Deleting the heading must not take the project filed under it. It is
	// ungrouped here, which is the case that would look identical either way if
	// the assertion were only "the group is gone".
	page.once('dialog', (dialog) => dialog.accept());
	await sidebar.getByRole('button', { name: 'Slet' }).click();
	await expect(sidebar.getByRole('link', { name: 'Kontoret', exact: true })).toBeHidden();
	await expect(sidebar.getByRole('link', { name: 'Regnskab' })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * A task can be dropped into a section, including one with nothing in it.
 *
 * Only the task *rows* took a drop, so a section with no rows had nothing to aim
 * at — and a section you have just made is always empty, which is exactly when
 * you want to fill it. The board has always taken a drop on the column; the list
 * had no equivalent.
 *
 * The second half is the other thing that made this hard to report: the page used
 * to say "Projektet findes ikke, eller du har ikke adgang til det" whenever the
 * load failed *or* had not finished, so a dropped connection and a project you
 * cannot see were one sentence, and it never asked again.
 */
test('en opgave kan trækkes ned i en tom sektion, og en fejlet indlæsning siger det', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Risteriet');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	// Waited for. Creating a project navigates, and `goto` is a promise nobody here
	// is holding — quick add fired before it landed puts the task in the Inbox with
	// no date, where the Today view is right not to show it. Intermittent, and it
	// reads as the quick add having failed.
	await expect(page.getByRole('heading', { name: 'Risteriet' })).toBeVisible();

	await page.getByLabel('Ny opgave').fill('brænde kaffe');
	await page.getByLabel('Ny opgave').press('Enter');
	await expect(page.getByText('brænde kaffe')).toBeVisible();

	await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
	await page.getByLabel('Ny sektion').fill('Uge 34');
	await page.getByLabel('Ny sektion').press('Enter');

	const section = page
		.locator('section')
		.filter({ has: page.getByRole('heading', { name: 'Uge 34' }) });
	await expect(section.getByText('Tom')).toBeVisible();

	await page.locator('.sortable').filter({ hasText: 'brænde kaffe' }).dragTo(section);
	await expect(section.getByText('brænde kaffe')).toBeVisible();
	await expect(page.locator('.toast')).toHaveCount(0);

	// A load that fails says so, and offers to try again.
	await page.route('**/api/v1/projects/*', (r) => r.abort());
	await page.reload();
	await expect(page.getByText('Serveren svarede ikke')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Prøv igen' })).toBeVisible();
	await page.unroute('**/api/v1/projects/*');
	await page.getByRole('button', { name: 'Prøv igen' }).click();
	await expect(page.getByRole('heading', { name: 'Risteriet' })).toBeVisible();

	expect(trouble.filter((t) => !t.includes('projects'))).toEqual([]);
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
	// Waited for before navigating away, or the create can lose its own redirect.
	await expect(sidebar.getByRole('link', { name: 'Ærinder' })).toBeVisible();

	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Liste', exact: true }).click();

	const day = (name) =>
		page.locator('section').filter({ has: page.getByRole('heading', { name, exact: true }) });
	const row = page.locator('[draggable="true"]').filter({ hasText: 'hent pakken' });

	await expect(day('I dag').getByText('hent pakken')).toBeVisible();
	await row.dragTo(day('I morgen'));
	await expect(day('I morgen').getByText('hent pakken')).toBeVisible();
	await expect(day('I dag').getByText('hent pakken')).toBeHidden();

	// Onto "I dag" in the sidebar: the most-made rescheduling there is, and the
	// sidebar is where the pointer already is. "Kommende" deliberately does not
	// take one — it is a range, not a day, so a drop would have to invent a date
	// its label does not name.
	await row.dragTo(sidebar.getByRole('link', { name: 'I dag' }));
	await expect(day('I dag').getByText('hent pakken')).toBeVisible();
	await row.dragTo(day('I morgen'));

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
	await page.getByRole('button', { name: 'Måned', exact: true }).click();

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

	// The invitee opens the project. Nothing has ever driven this: the assertion
	// above only proved the project reaches their *sidebar*, and a member's view
	// of a project page is a different code path from an owner's — different role,
	// different guards, and the page shows the same "findes ikke" message while it
	// is still loading as it does when the load failed.
	const inviteePage = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const member = await inviteePage.newPage();
	const memberTrouble = watchForTrouble(member);
	await member.goto('/');
	await member.getByLabel('E-mail').fill('nabo@example.dk');
	await member.getByLabel('Adgangskode').fill('et langt kodeord til test');
	await member.getByRole('button', { name: 'Log ind' }).click();

	const theirs = member.getByRole('navigation', { name: 'Hovedmenu' });
	await theirs.getByRole('link', { name: /Fælleshuset/ }).click();
	await expect(member.getByRole('heading', { name: 'Fælleshuset' })).toBeVisible();
	await expect(member.getByText('findes ikke')).toHaveCount(0);
	expect(memberTrouble.filter((t) => t !== 'GET /api/v1/auth/me → 401')).toEqual([]);
	await inviteePage.close();

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
	// The task's own text, not the whole row's: quick add read "fredag" as the due
	// date, so the title is "males inden" and the rest of that line is metadata —
	// which now has the assignee in it too.
	await expect(page.getByText('males inden', { exact: true })).toBeVisible();

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
 * The device list, and ending one of them.
 *
 * `last_seen_at` has been written on every request since sessions existed — once
 * a minute, deliberately, so this list could say "lige nu" — and until now
 * nothing read it. A session you cannot see is a session you cannot end, which
 * matters most in the one situation it exists for: somebody else has your cookie.
 */
test('enhedslisten viser denne enhed, og en anden kan logges ud', async ({ browser, page }) => {
	const trouble = watchForTrouble(page);

	// A second sign-in as the same person: a real second session, not a second tab
	// sharing this one's cookie.
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const elsewhere = await context.newPage();
	await elsewhere.goto('/');
	await elsewhere.getByLabel('E-mail').fill(USER.email);
	await elsewhere.getByLabel('Adgangskode').fill(USER.password);
	await elsewhere.getByRole('button', { name: 'Log ind' }).click();
	await expect(elsewhere.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	await page.goto('/indstillinger');
	const panel = page.locator('section.panel').filter({ hasText: 'Enheder' });
	await expect(panel.getByText('denne enhed')).toBeVisible();
	await expect(panel.locator('li')).toHaveCount(2);

	// End the one that is not this browser — the row without the badge.
	await panel.locator('li').filter({ hasNot: page.getByText('denne enhed') })
		.getByRole('button', { name: 'Log ud' })
		.click();
	await expect(panel.locator('li')).toHaveCount(1);

	// That session is really gone, and this one is untouched.
	await elsewhere.reload();
	await expect(elsewhere.getByText('Log ind for at fortsætte')).toBeVisible();
	await context.close();

	await expect(panel.getByText('denne enhed')).toBeVisible();
	expect(trouble).toEqual([]);
});

/**
 * An administrator can put somebody on the instance without sharing a project
 * with them first.
 *
 * This is the answer to "how do I create a user": there is no open registration
 * and no account with a password somebody else chose, so adding a person means
 * issuing an invite that carries no project. The whole loop is worth driving —
 * the link, a second browser, the signup, and the new account showing up in the
 * list as somebody who is *not* an administrator.
 */
test('en administrator kan invitere en bruger til instansen', async ({ browser, page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger/brugere');

	await page.getByLabel('E-mailadresse').fill('anders@example.dk');
	await page.getByRole('button', { name: 'Send invitation' }).click();

	const link = await page.locator('.link-out').textContent();
	expect(link).toContain('/invite?token=');
	await expect(page.getByText('anders@example.dk')).toBeVisible();
	await expect(page.getByText('til instansen')).toBeVisible();

	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const invitee = await context.newPage();
	await invitee.goto(link);
	await invitee.getByLabel('Navn').fill('Anders');
	await invitee.getByLabel(/Adgangskode/).fill('et langt kodeord til test');
	await invitee.getByRole('button', { name: 'Opret konto' }).click();

	// They are in — with an Inbox of their own, and no project shared with them,
	// which is the difference between this and a project invite.
	await expect(invitee.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();
	await expect(invitee.getByRole('heading', { name: 'I dag' })).toBeVisible();
	await context.close();

	await page.reload();
	const konti = page.locator('section.panel').filter({ hasText: 'Konti' });
	const row = konti.locator('li').filter({ hasText: 'Anders' });
	await expect(row).toBeVisible();
	// Invited, not promoted. Asserted through the button that offers to promote
	// them — which only reads this way for somebody who is not one. Matching the
	// word "administrator" in the row would match that very button.
	await expect(row.getByRole('button', { name: 'Gør til administrator' })).toBeVisible();
	// And the invite is spent rather than left live.
	await expect(page.getByText('Afventer svar')).toBeHidden();

	expect(trouble).toEqual([]);
});

/**
 * The two refusals that keep an instance administrable. Driven here as well as in
 * the API tests because the interface has to *say* why, and a 409 nobody explains
 * is a button that appears broken.
 */
test('den sidste administrator kan hverken fjernes eller slette sig selv', async ({ page }) => {
	await page.goto('/indstillinger/brugere');

	const konti = page.locator('section.panel').filter({ hasText: 'Konti' });
	const mine = konti.locator('li').filter({ hasText: 'dig' });
	await expect(mine).toBeVisible();

	// No buttons on your own row: the server refuses both, and a button whose only
	// purpose is to be refused wastes a click to teach a rule.
	await expect(mine.getByRole('button')).toHaveCount(0);
});

/**
 * A member's role can be corrected without removing them.
 *
 * This is what a viewer who cannot add tasks looks like from the other side. The
 * rule itself is right — a viewer may not write — but until now the only way to
 * fix an invite sent with the wrong role was to remove the person and invite them
 * again, which unassigns every task they were responsible for on the way past.
 *
 * Driven end to end because the interesting part is that the *other* browser's
 * permissions actually change: the server decides, and the page has to agree.
 */
test('en rolle kan rettes uden at fjerne personen', async ({ browser, page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Fælles');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	// Waited for, like everywhere else here: creating a project navigates, and the
	// controls below only exist once it has.
	await expect(page.getByRole('heading', { name: 'Fælles' })).toBeVisible();

	await page.getByRole('button', { name: 'Del' }).click();
	await page.getByLabel('Inviter via e-mail').fill('andreas@example.dk');
	await page.getByLabel('Rolle').selectOption('viewer');
	await page.getByRole('button', { name: 'Inviter' }).click();
	const link = await page.locator('.link-out code').textContent();

	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const andreas = await context.newPage();
	await andreas.goto(link);
	await andreas.getByLabel('Navn').fill('andreas');
	await andreas.getByLabel(/Adgangskode/).fill('et langt kodeord til test');
	await andreas.getByRole('button', { name: 'Opret konto' }).click();
	await expect(andreas.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	await andreas
		.getByRole('navigation', { name: 'Hovedmenu' })
		.getByRole('link', { name: /Fælles/ })
		.click();
	// A viewer sees the project and is told, plainly, that it is read-only.
	await expect(andreas.getByText('Du kan se dette projekt, men ikke ændre det')).toBeVisible();
	await expect(andreas.getByLabel('Ny opgave')).toHaveCount(0);

	// The owner promotes them from the member list. The owner's own row is text,
	// not a dropdown: ownership is transferred, not granted.
	await page.reload();
	await page.getByRole('button', { name: /Delt/ }).click();
	await expect(page.getByText('Ejer')).toBeVisible();
	await page.getByLabel('Rolle for andreas').selectOption('editor');

	await andreas.reload();
	await expect(andreas.getByLabel('Ny opgave')).toBeVisible();
	await expect(andreas.getByText('Du kan se dette projekt')).toHaveCount(0);

	// Now that they can edit, hand them something — and the row says whose it is.
	// Only when it is somebody else's: your own list would otherwise carry your own
	// face on every line, which is a column of one initial telling you nothing.
	await page.getByLabel('Ny opgave').fill('brænde kaffe');
	await page.getByLabel('Ny opgave').press('Enter');
	await page.getByText('brænde kaffe', { exact: true }).click();
	const drawer = page.getByRole('complementary', { name: 'Opgave' });
	await drawer.getByLabel('Ansvarlig').selectOption({ label: 'andreas' });
	await page.keyboard.press('Escape');

	const row = page.locator('.row').filter({ hasText: 'brænde kaffe' });
	await expect(row.getByTitle('Ansvarlig: andreas')).toBeVisible();
	// And not on a task that is nobody's.
	await page.getByLabel('Ny opgave').fill('min egen');
	await page.getByLabel('Ny opgave').press('Enter');
	await expect(
		page.locator('.row').filter({ hasText: 'min egen' }).locator('.assignee')
	).toHaveCount(0);

	// The log has recorded all of this since the beginning and nothing showed it.
	await page.getByRole('button', { name: 'Historik' }).click();
	await expect(page.getByText('oprettede projektet')).toBeVisible();
	await expect(page.getByText('ændrede rollen for')).toBeVisible();

	await context.close();
	expect(trouble).toEqual([]);
});

/**
 * A task dragged across a month boundary.
 *
 * The month grid could not do this. It is anchored to a month, so the two days on
 * either side of its edge sit in different grids, and getting from one to the other
 * means paging — which cannot be done mid-drag, because a drag in flight swallows
 * the click that would page it. The week view exists for exactly this: a week that
 * straddles the 31st and the 1st has both days in the same row.
 *
 * The dates are worked out in the browser, not here. Playwright pins the page to
 * Europe/Copenhagen and this process is not pinned to anything, so for two hours of
 * every day the two disagree about what today is.
 */
test('en opgave kan trækkes hen over et månedsskifte i uge-visningen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	// The first day, starting tomorrow, whose next day is in another month *and* in
	// the same Monday-to-Sunday week. Such a pair exists at every month boundary
	// except one that falls between a Sunday and a Monday, so searching forward
	// finds one within about two months whatever today is.
	const { from, to } = await page.evaluate(() => {
		const iso = (d) =>
			`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
				d.getDate()
			).padStart(2, '0')}`;
		const monday = (d) => {
			const m = new Date(d.getFullYear(), d.getMonth(), d.getDate());
			m.setDate(m.getDate() - ((m.getDay() + 6) % 7));
			return iso(m);
		};
		const day = new Date();
		for (let i = 0; i < 90; i++) {
			day.setDate(day.getDate() + 1);
			const next = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
			if (day.getMonth() !== next.getMonth() && monday(day) === monday(next)) {
				return { from: iso(day), to: iso(next) };
			}
		}
		throw new Error('no month boundary inside a week in the next 90 days');
	});

	const box = page.getByLabel('Ny opgave');
	await box.fill(`skifte dæk ${from}`);
	await box.press('Enter');
	// Not asserted here: this is the Today view and the task is dated weeks out, so
	// it is correctly absent. The week grid below is where it has to turn up.
	await expect(box).toHaveValue('');

	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Uge', exact: true }).click();

	// Forward a week at a time until the row is the one holding that date. Bounded,
	// because a loop that pages forever on a broken button is a timeout with no
	// explanation in it.
	const cell = page.locator(`[data-date="${from}"]`);
	const next = page.getByRole('button', { name: 'Næste uge' });
	for (let i = 0; i < 15 && (await cell.count()) === 0; i++) {
		await next.click();
	}
	await expect(cell, `uge-visningen nåede aldrig frem til ${from}`).toBeVisible();

	// The claim the view is for: both sides of the month boundary, in one row.
	await expect(page.locator(`[data-date="${to}"]`)).toBeVisible();
	expect(from.slice(0, 7), 'de to dage skulle ligge i hver sin måned').not.toEqual(to.slice(0, 7));

	const chip = page.locator('.chip').filter({ hasText: 'skifte dæk' });
	await expect(chip).toBeVisible();
	await chip.dragTo(page.locator(`[data-date="${to}"]`));

	await expect(page.locator(`[data-date="${to}"]`).getByText('skifte dæk')).toBeVisible();
	await expect(page.locator(`[data-date="${from}"]`).getByText('skifte dæk')).toBeHidden();

	expect(trouble).toEqual([]);
});

/**
 * The instance-wide history.
 *
 * The per-project panel has shown the same rows for a while; this is the question
 * it cannot answer, which is what happened in a project the administrator is not a
 * member of. The test uses a second person's own project for exactly that reason —
 * asserting against a project the admin can already see would pass with the old
 * per-project endpoint behind it.
 */
test('historik-siden viser hændelser fra et projekt, administratoren ikke er med i', async ({
	browser,
	page
}) => {
	const trouble = watchForTrouble(page);

	// A second account, with a project of its own. storageState is spelled out:
	// a "fresh" context inherits the signed-in one otherwise, which is the single
	// case this test is not about.
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const theirs = await context.newPage();
	const email = `historik-${Date.now()}@example.dk`;

	await page.goto('/indstillinger/brugere');
	await page.getByLabel('E-mailadresse').fill(email);
	await page.getByRole('button', { name: 'Send invitation' }).click();
	const link = await page.locator('.link-out').textContent();
	expect(link).toContain('/invite?token=');

	await theirs.goto(link);
	await theirs.getByLabel('Navn').fill('Sigrid');
	await theirs.getByLabel(/Adgangskode/).fill('et langt kodeord til test');
	await theirs.getByRole('button', { name: 'Opret konto' }).click();
	await expect(theirs.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	const sidebar = theirs.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Sigrids eget');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Sigrids eget' })).toBeVisible();

	// The administrator cannot open that project, and can still see that it happened.
	await page.goto('/indstillinger/historik');
	const rows = page.locator('.rows li').filter({ hasText: 'Sigrids eget' });
	await expect(rows.first()).toBeVisible();
	await expect(rows.first().getByText('oprettede projektet')).toBeVisible();
	// `exact`, or this matches the project name "Sigrids eget" in the same row.
	await expect(rows.first().getByText('Sigrid', { exact: true })).toBeVisible();

	// And the filters narrow it rather than emptying it.
	await page.getByLabel('Hændelse').selectOption('project.created');
	await expect(page.locator('.rows li').filter({ hasText: 'Sigrids eget' }).first()).toBeVisible();
	await page.getByRole('button', { name: 'Ryd' }).click();

	await context.close();
	expect(trouble).toEqual([]);
});

/**
 * The project page on a phone.
 *
 * Every other test here runs at desktop width, and this is what that misses: the
 * header is a flex row of a title and four controls, and 390px does not have room
 * for them. Without wrapping, the heading was squeezed to its min-content — which
 * with `overflow-wrap: anywhere` is one character — so a project called
 * "Skovvænget" rendered as a vertical column of letters while the buttons ran off
 * the right edge. It looked like a broken font.
 */
test('projektsiden er brugbar på en telefon', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');

	// The sidebar is a drawer at this width, so the project is reached through it.
	await page.getByRole('button', { name: 'Vis menu' }).click();
	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Skovvænget');
	await sidebar.getByLabel('Projektnavn').press('Enter');

	const heading = page.getByRole('heading', { name: 'Skovvænget' });
	await expect(heading).toBeVisible();

	const box = await heading.boundingBox();
	// Wide enough to hold the word, and short enough not to be a column of
	// letters. One line of "Skovvænget" at --text-2xl is nowhere near 200px tall.
	expect(box.width, 'overskriften er klemt sammen').toBeGreaterThan(120);
	expect(box.height, 'overskriften er brudt op i én bogstavkolonne').toBeLessThan(120);

	// And nothing sticks out past the right edge of the page.
	const sideways = () =>
		page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
	expect(await sideways(), 'siden kan scrolles vandret').toBeLessThanOrEqual(1);

	// The task drawer, too. It was `width: 100vw`, which includes the scrollbar
	// gutter where a percentage does not, so the drawer came out wider than the
	// page and the whole document could be pushed sideways.
	await page.getByLabel('Ny opgave').fill('mal væggen');
	await page.getByLabel('Ny opgave').press('Enter');
	await page.getByText('mal væggen', { exact: true }).click();
	await expect(page.getByRole('complementary', { name: 'Opgave' })).toBeVisible();
	expect(await sideways(), 'opgaveruden skubber siden sidelæns').toBeLessThanOrEqual(1);

	expect(trouble).toEqual([]);
});

/**
 * A group is somewhere you can go.
 *
 * It was a heading over some rows: a name, a colour, and whether it was folded.
 * Enough to tidy a list and not enough to be what people mean by it — "Arbejde" is
 * a body of work with context of its own, and the documents that belong to all of
 * it rather than to any one project inside it.
 */
test('en gruppe er en side med sine projekter, en beskrivelse og filer', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Ny gruppe' }).click();
	await sidebar.getByLabel('Gruppens navn').fill('Værksted');
	await sidebar.getByLabel('Gruppens navn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Værksted' })).toBeVisible();

	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Drejebænk');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Drejebænk' })).toBeVisible();

	await page.getByLabel('Ny opgave').fill('bestil stål');
	await page.getByLabel('Ny opgave').press('Enter');
	await expect(page.getByText('bestil stål', { exact: true })).toBeVisible();

	// Into the group, then onto its page — which is what the heading is a link to
	// now. The chevron beside it still folds; they are two intentions and the old
	// single target could only serve the smaller one.
	await sidebar
		.getByRole('link', { name: 'Drejebænk' })
		.dragTo(sidebar.getByRole('link', { name: 'Værksted' }));
	await sidebar.getByRole('link', { name: 'Værksted' }).click();

	await expect(page.getByRole('heading', { name: 'Værksted', level: 1 })).toBeVisible();
	const row = page.locator('.projects a').filter({ hasText: 'Drejebænk' });
	await expect(row).toBeVisible();
	await expect(row.getByText('1', { exact: true }), 'antallet af åbne opgaver').toBeVisible();

	// The description, which is the part a heading cannot carry. Click to write,
	// blur to save — the same shape a task's description has.
	await page.getByRole('button', { name: /Skriv hvad det her er/ }).click();
	const about = page.getByLabel('Om gruppen');
	await expect(about).toBeVisible();
	await about.fill('Alt der larmer og støver.');
	await about.blur();
	await expect(page.getByText('Alt der larmer og støver.')).toBeVisible();

	// And it survives a reload, which is the difference between the page keeping up
	// and the server taking it.
	await page.reload();
	await expect(page.getByText('Alt der larmer og støver.')).toBeVisible();
	await expect(page.locator('.projects a').filter({ hasText: 'Drejebænk' })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * The account's language changes the interface, not only the parser.
 *
 * `locale` has been on the account since the beginning and only chose which
 * grammar quick add read a line with — so picking English changed nothing anybody
 * could see, and the field said so honestly: "Sprog i hurtig tilføjelse".
 *
 * Asserted on both directions and on a date, because the date formatter is the
 * half that is easy to leave behind: `toLocaleDateString('da-DK', …)` was written
 * into a dozen components, and an English interface still saying "24. aug." is the
 * shape of a half-finished translation.
 *
 * It puts Danish back at the end. Everything after this reads Danish labels, and a
 * test that leaves the account in English fails the ones that follow it rather
 * than itself — which is the worst way for a suite to report anything.
 */
test('kontoens sprog skifter hele fladen, ikke kun parseren', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await expect(sidebar.getByRole('link', { name: 'I dag' })).toBeVisible();

	await page.getByLabel('Sprog').selectOption('en');
	await page.getByRole('button', { name: 'Gem' }).first().click();

	// The sidebar redraws without a reload: i18n.locale is $state, which is the
	// whole reason that module is .svelte.js.
	const english = page.getByRole('navigation', { name: 'Main menu' });
	await expect(english.getByRole('link', { name: 'Today' })).toBeVisible();
	await expect(english.getByRole('link', { name: 'Upcoming' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();

	// And it survives a reload, which is the difference between the page keeping up
	// and the account actually holding it.
	await page.reload();
	await expect(
		page.getByRole('navigation', { name: 'Main menu' }).getByRole('link', { name: 'Today' })
	).toBeVisible();

	// A date, in the half that is easy to leave in Danish. The month grid's heading
	// is `toLocaleDateString`, which was `'da-DK'` in a dozen components before
	// `tag()` existed — an English interface still saying "december" is exactly the
	// shape of a half-finished translation.
	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Month', exact: true }).click();
	await expect(page.locator('.calendar h2')).not.toContainText(
		/januar|februar|marts|maj|juni|juli|oktober|december/i
	);

	// The Inbox keeps its name: it is a project row created at signup, not a
	// string in the interface, and the person can rename it like any other.

	// Danish again, for everything that runs after this.
	await page.goto('/indstillinger');
	await page.getByLabel('Language').selectOption('da');
	await page.getByRole('button', { name: 'Save' }).first().click();
	await expect(
		page.getByRole('navigation', { name: 'Hovedmenu' }).getByRole('link', { name: 'I dag' })
	).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * Finished tasks can be looked at, and are out of the way by default.
 *
 * A task list is what is left to do, so closing something has to remove it from
 * the plan — but "what did I get done" is a real question and the answer was
 * nowhere. They come back in one list at the bottom rather than back among the
 * open ones in their sections: a closed task has stopped being work and started
 * being a record, and putting the record back in the middle makes the plan longer
 * without making it say more.
 *
 * The setting is in localStorage, so this also checks it survives a reload — the
 * half that a toggle held only in component state would fail.
 */
test('færdige opgaver kan vises og skjules igen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Loftet');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Loftet' })).toBeVisible();

	for (const what of ['rydde op', 'male gavlen']) {
		await page.getByLabel('Ny opgave').fill(what);
		await page.getByLabel('Ny opgave').press('Enter');
		await expect(page.getByText(what, { exact: true })).toBeVisible();
	}

	// Close one. It leaves the list, which is the behaviour being protected here.
	await page
		.locator('.row')
		.filter({ hasText: 'rydde op' })
		.getByRole('button', { name: 'Markér som færdig' })
		.click();
	await expect(page.getByText('rydde op', { exact: true })).toBeHidden();
	await expect(page.getByText('male gavlen', { exact: true })).toBeVisible();

	// And it can be looked at again, under its own heading at the bottom.
	await page.getByRole('button', { name: 'Vis færdige' }).click();
	const done = page.locator('section.done');
	await expect(done.getByRole('heading', { name: 'Færdige' })).toBeVisible();
	await expect(done.getByText('rydde op', { exact: true })).toBeVisible();
	// Still exactly where it was in the plan — reopened tasks are the only ones
	// that come back up.
	await expect(done.getByText('male gavlen', { exact: true })).toHaveCount(0);

	// The choice is in localStorage, so it holds across a reload.
	await page.reload();
	await expect(page.locator('section.done').getByText('rydde op', { exact: true })).toBeVisible();

	await page.getByRole('button', { name: 'Skjul færdige' }).click();
	await expect(page.locator('section.done')).toHaveCount(0);
	await expect(page.getByText('male gavlen', { exact: true })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * The sidebar never scrolls sideways.
 *
 * It grew a horizontal scrollbar on an ordinary desktop, under the whole menu.
 * The cause was one flex item: the account name had `overflow: hidden` and no
 * `min-width: 0`, so it refused to shrink below its content and pushed the row
 * past the column — and `overflow-y: auto` on the sidebar resolves to `auto` on
 * the other axis too, which turned "a row that is too wide" into a scrollbar.
 *
 * Asserted with a long name, because the name is the part that varies and a short
 * one hides the whole class of bug. The mobile test measures the *document*; this
 * measures the sidebar itself, which is a different box and was never checked.
 */
test('sidebjælken kan ikke scrolles vandret', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger');

	// A name long enough to overflow the column at its default width.
	await page.getByLabel('Navn').fill('Kristian Vinterberg-Skovgaard');
	await page.getByRole('button', { name: 'Gem' }).first().click();
	await expect(page.getByText('Kristian Vinterberg-Skovgaard').first()).toBeVisible();

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	const sideways = await sidebar.evaluate((el) => el.scrollWidth - el.clientWidth);
	expect(sideways, 'sidebjælken kan scrolles vandret').toBeLessThanOrEqual(1);

	// And the whole page, for the same reason at a different scale.
	const page_sideways = await page.evaluate(
		() => document.documentElement.scrollWidth - document.documentElement.clientWidth
	);
	expect(page_sideways, 'siden kan scrolles vandret').toBeLessThanOrEqual(1);

	// Put the name back, so the tests after this one see the account they expect.
	await page.getByLabel('Navn').fill(USER.name);
	await page.getByRole('button', { name: 'Gem' }).first().click();

	expect(trouble).toEqual([]);
});

/**
 * A project can have more than one section.
 *
 * Reported from use: sections "have no function, because you cannot create more
 * than one". The API makes as many as you ask for — there is a Go test that makes
 * three — so whatever this is, it is in the page. Every other test here makes
 * exactly one section, which is the shape of test that cannot see it.
 */
test('et projekt kan have flere sektioner @forms', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Ombygning');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Ombygning' })).toBeVisible();

	for (const name of ['Planlægning', 'I gang', 'Til gennemsyn']) {
		await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
		const field = page.getByLabel('Ny sektion');
		await expect(field, `feltet kom ikke frem for "${name}"`).toBeVisible();
		await field.fill(name);
		await field.press('Enter');
		await expect(
			page.getByRole('heading', { name, exact: true }),
			`sektionen "${name}" kom ikke på siden`
		).toBeVisible();
	}

	// All three at once, still there — an each-block that threw would have stopped
	// rendering at the one that broke it.
	for (const name of ['Planlægning', 'I gang', 'Til gennemsyn']) {
		await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
	}

	// And they survive a reload, which is the difference between "the page kept up"
	// and "the server took it".
	await page.reload();
	for (const name of ['Planlægning', 'I gang', 'Til gennemsyn']) {
		await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
	}

	// The board is where somebody actually looks for a section, because a column on
	// a board *is* one — and it had no way to make one at all. Reported as
	// "sections have no function: there is no way to create them", which from
	// inside a board was exactly true.
	await page.getByRole('button', { name: 'Board', exact: true }).click();
	await expect(page.getByRole('heading', { name: 'Uden sektion' })).toBeVisible();
	await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
	const field = page.getByLabel('Ny sektion');
	await expect(field, 'boardet har ingen måde at lave en sektion på').toBeVisible();
	await field.fill('Afsluttet');
	await field.press('Enter');
	await expect(page.getByRole('heading', { name: 'Afsluttet', exact: true })).toBeVisible();

	await page.reload();
	await expect(page.getByRole('heading', { name: 'Afsluttet', exact: true })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * Form fields on a touch device are at least 16px.
 *
 * Not a matter of taste. Below 16px, iOS Safari zooms the page in when a field
 * takes focus and does not zoom back out when it is blurred — so the person is
 * left with the whole interface too large and has to pinch it back by hand, once
 * per field they tap. The type scale tops out at 15px, so every input in the app
 * did it; the task drawer worst, being four fields deep.
 *
 * Chromium cannot reproduce the zoom, but it can be asked the question that causes
 * it, which is what this checks. A touch context rather than a narrow viewport:
 * the rule is keyed on `(hover: none) and (pointer: coarse)`, because an iPad does
 * the same thing at a width no phone breakpoint would catch.
 */
test('felter er mindst 16px, så iOS ikke zoomer ind og bliver der', async ({ browser }) => {
	const phone = { viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true };

	const tooSmall = async (page, where) => {
		const bad = await page.evaluate(() =>
			[...document.querySelectorAll('input, textarea, select')]
				.filter((el) => el.getClientRects().length)
				.map((el) => ({
					what: el.tagName.toLowerCase() + (el.type ? `[type=${el.type}]` : ''),
					size: parseFloat(getComputedStyle(el).fontSize)
				}))
				.filter((f) => f.size < 16)
		);
		expect(bad, `for små felter på ${where}`).toEqual([]);
	};

	// The sign-in form, in a context with the session spelled out as empty —
	// newContext inherits the project's storageState, so a "fresh" browser is
	// otherwise already signed in and the form never renders.
	const signedOut = await browser.newContext({
		...phone,
		storageState: { cookies: [], origins: [] }
	});
	const anon = await signedOut.newPage();
	await anon.goto('/');
	await expect(anon.getByRole('button', { name: 'Log ind' })).toBeVisible();
	await tooSmall(anon, 'login-siden');
	await signedOut.close();

	const context = await browser.newContext(phone);
	const page = await context.newPage();
	const trouble = watchForTrouble(page);

	await page.goto('/');
	await expect(page.getByLabel('Ny opgave')).toBeVisible();
	await tooSmall(page, 'I dag');

	// The task drawer, which is where it was worst: four fields deep. Dated today,
	// or it goes to the Inbox and is correctly absent from the view being looked at.
	await page.getByLabel('Ny opgave').fill('slibe gulvet i dag');
	await page.getByLabel('Ny opgave').press('Enter');
	await page.getByText('slibe gulvet', { exact: true }).click();
	await expect(page.getByRole('complementary', { name: 'Opgave' })).toBeVisible();
	await tooSmall(page, 'opgavedetaljer');

	// And the settings pages, which are almost entirely fields.
	await page.goto('/indstillinger');
	await expect(page.locator('section.panel').first()).toBeVisible();
	await tooSmall(page, 'indstillinger');

	await context.close();
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
