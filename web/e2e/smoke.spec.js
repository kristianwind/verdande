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
/**
 * Opens the project header's overflow menu and clicks one of its items.
 *
 * The header used to carry eight controls in one row — title, colour, show-done,
 * three view buttons, share, history, delete — and a project name is not a short
 * word, so the heading was what gave way. Everything but the view switcher moved
 * behind one button; this is how a test reaches them.
 */
async function projectAction(page, name) {
	await page.getByRole('button', { name: 'Flere handlinger' }).click();
	// `exact` only means anything for a string. The share item reads "Delt · 2" once
	// somebody is in the project, so callers pass a regex for that one.
	const item =
		name instanceof RegExp
			? page.getByRole('menuitem', { name })
			: page.getByRole('menuitem', { name, exact: true });
	await item.click();
}

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

test('en URL i en opgave bliver et klikbart link, og resten åbner opgaven', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	// The URL is one token, so the parser leaves it in the title; "i dag" is what it
	// takes. The link is made at display time — the stored title is still plain text.
	await box.fill('læs https://example.com/artikel i dag');
	await box.press('Enter');

	const row = page.locator('.row').filter({ hasText: 'læs' });
	const link = row.getByRole('link', { name: 'https://example.com/artikel' });
	await expect(link).toHaveAttribute('href', 'https://example.com/artikel');
	await expect(link).toHaveAttribute('rel', /noopener/);

	// Clicking the plain part of the title still opens the task; only the link is
	// carved out. Aimed at the top-left, over "læs", not the URL in the middle.
	await row.locator('.content').click({ position: { x: 3, y: 3 } });
	await expect(page.getByRole('complementary', { name: 'Opgave' })).toBeVisible();

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

test('en opgave kan slumres og hentes frem igen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	// "i dag" is parsed as the due date, so the task is named "Alfa" and shows here.
	const box = page.getByLabel('Ny opgave');
	await box.fill('Alfa i dag');
	await box.press('Enter');

	// Snooze it to tomorrow from its drawer.
	await page.getByText('Alfa', { exact: true }).click();
	const drawer = page.getByRole('complementary', { name: 'Opgave' });
	await drawer.getByRole('button', { name: 'I morgen' }).click();

	// The row greys — it carries the snoozed class and a "slumret til" mark.
	const alfa = page.locator('.row', { hasText: 'Alfa' });
	await expect(alfa).toHaveClass(/snoozed/);
	await expect(alfa.locator('.snooze-mark')).toBeVisible();

	// The drawer now offers to wake it, and waking takes the grey off.
	await drawer.getByRole('button', { name: 'Væk nu' }).click();
	await expect(alfa).not.toHaveClass(/snoozed/);

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

test('menu- og brødtekststørrelse sættes hver for sig og overlever en genindlæsning', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger');

	// Two independent knobs under Udseende — the sidebar's size, and the body text's.
	await page.getByLabel('Menu', { exact: true }).selectOption('large');
	await page.getByLabel('Brødtekst', { exact: true }).selectOption('xl');
	await expect(page.getByLabel('Menu', { exact: true })).toHaveValue('large');

	const attrs = () =>
		page.evaluate(() => [
			document.documentElement.dataset.menuSize,
			document.documentElement.dataset.textSize
		]);
	expect(await attrs()).toEqual(['large', 'xl']);

	// They live in localStorage like the theme, so they survive a reload — and are
	// applied before first paint, so the picker comes back showing the saved choice.
	await page.reload();
	expect(await attrs()).toEqual(['large', 'xl']);
	await expect(page.getByLabel('Menu', { exact: true })).toHaveValue('large');

	// Put them back, so the shared account does not leak a size into other tests.
	await page.getByLabel('Menu', { exact: true }).selectOption('default');
	await page.getByLabel('Brødtekst', { exact: true }).selectOption('default');
	expect(await attrs()).toEqual([undefined, undefined]);

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
	await projectAction(page, 'Slet projektet');

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

	// A group's name must be readable in full, and must start where a project's
	// name starts.
	//
	// Both halves are here because both were once broken at the same time and for
	// different reasons. "Omdøb" and "Slet" sat in the flow reserving their width
	// even while invisible, so ARBEJDE was drawn as "ARBE"; and the fold chevron
	// sat before the name, pushing a group 22px further in than the projects it
	// contains — so a group read as if it were filed under them. Every assertion
	// in this file passed through both, because both buttons still worked.
	const shape = await sidebar.locator('.folder-head').evaluate((el) => {
		const link = el.querySelector('h2 a');
		const name = { ...link.querySelector('.dot').getBoundingClientRect().toJSON() };
		// The last child of the link is the name itself; the dot is a span before it.
		return {
			clipped: link.scrollWidth > link.clientWidth + 1,
			left: name.left,
			right: link.getBoundingClientRect().right,
			count: el.querySelector('.count')?.getBoundingClientRect().left ?? Infinity
		};
	});

	expect(shape.clipped, 'gruppens navn bliver klippet').toBe(false);
	expect(shape.count, 'navnet og tallet overlapper').toBeGreaterThanOrEqual(shape.right);

	// Measured dot to dot, not box to box: both rows begin with the same coloured
	// mark, and it is the only thing in them that means the same on both sides.
	// Comparing the group's inner link with the project's outer box reported the
	// row's own padding as a misalignment.
	const projectLeft = await sidebar
		.getByRole('link', { name: 'Regnskab' })
		.evaluate((el) => el.querySelector('.dot').getBoundingClientRect().left);
	expect(
		Math.abs(shape.left - projectLeft),
		`gruppen starter ${Math.round(shape.left - projectLeft)}px fra projekterne`
	).toBeLessThanOrEqual(2);

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

	// En foldet gruppe står med sin chevron fremme permanent, og den lå oven i
	// prikken med et felt i --surface bag sig — en lys firkant ud for navnet på
	// den sænkede bjælke, hele tiden. Nu tones prikken ud i stedet, så der er
	// ingenting at male henover den med.
	const chevron = await sidebar
		.locator('.folder-head .chevron-button')
		.evaluate((el) => ({
			background: getComputedStyle(el).backgroundColor,
			dot: getComputedStyle(el.closest('.folder-head').querySelector('.group-dot')).opacity
		}));
	expect(chevron.background, 'chevronen har en firkant bag sig').toBe('rgba(0, 0, 0, 0)');
	expect(chevron.dot, 'prikken ligger stadig under chevronen').toBe('0');

	await sidebar.getByRole('button', { name: /^Fold Arbejde/ }).click();

	// Colour, which is chosen while renaming rather than from the row. Both are
	// edits to the same thing, and a heading carrying four controls crowded the
	// name it exists to show.
	// Double-click on the row opens it; the two words that used to sit on top of
	// the name are gone from the sidebar entirely.
	await sidebar.getByRole('link', { name: 'Arbejde', exact: true }).dblclick();
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
	await sidebar.getByRole('link', { name: 'Arbejde', exact: true }).dblclick();
	const name = sidebar.getByLabel('Gruppens navn');
	await name.fill('Kontoret');
	await name.press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Kontoret', exact: true })).toBeVisible();

	// Deleting the heading must not take the project filed under it. It is
	// ungrouped here, which is the case that would look identical either way if
	// the assertion were only "the group is gone".
	// Deleting is on the group's own page now, where the projects it will not take
	// with it are on the screen above the button.
	await sidebar.getByRole('link', { name: 'Kontoret', exact: true }).click();
	page.once('dialog', (dialog) => dialog.accept());
	await page.getByRole('button', { name: 'Slet' }).click();
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

	// Taller than the default window, and it is not cosmetic: the month grid is
	// six rows, and in 720 px the last one is cut off by the fold. A synthetic
	// drag drops at the target's centre without the auto-scroll a real pointer
	// gets from the browser — so a target below the fold is never hit.
	//
	// That made this test depend on the date. The task sits on tomorrow, and when
	// tomorrow is a Sunday, the day after is the first cell of the next row, down
	// in the cut-off part. It passed for months and failed the morning tomorrow
	// happened to be a Sunday, on a commit that had touched nothing near a
	// calendar.
	await page.setViewportSize({ width: 1280, height: 1000 });
	await page.getByRole('button', { name: 'Måned', exact: true }).click();

	const chip = page.locator('.chip').filter({ hasText: 'hent pakken' });
	await expect(chip).toBeVisible();

	const from = await page.locator('.day').filter({ has: chip }).getAttribute('data-date');

	// The neighbouring cell is taken from the grid, not worked out from the date.
	// The day after tomorrow is not always in the month on screen — at the end of
	// a short month it falls outside it entirely — and the cell next door always
	// is. Falls back to the cell before, for the one day a month when there is no
	// cell after.
	const dates = await page
		.locator('.day[data-date]')
		.evaluateAll((cells) => cells.map((cell) => cell.dataset.date));
	const at = dates.indexOf(from);
	const to = dates[at + 1] ?? dates[at - 1];

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

	await projectAction(page, 'Del');
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
	await invitee.getByLabel('Navn', { exact: true }).fill('Nabo');
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
	// Titlen alene, ikke rækken med datomærket i.
	//
	// Hurtig tilføjelse læser "fredag" som forfaldsdatoen og tager ordet ud af
	// titlen, så rækken er "males inden" plus et mærke, der siger hvornår. Mærkets
	// ord skifter med ugedagen — "fredag", når der er langt, og "I morgen", når man
	// kører prøven på en torsdag. Den her linje ledte efter hele rækken og fandt
	// den derfor ikke én dag om ugen. Prøven handler om invitationer, ikke om
	// datoer; den skal ikke vide hvilken dag det er.
	await page.getByText('males inden', { exact: true }).click();

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
	await invitee.getByLabel('Navn', { exact: true }).fill('Anders');
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

	await projectAction(page, 'Del');
	await page.getByLabel('Inviter via e-mail').fill('andreas@example.dk');
	await page.getByLabel('Rolle').selectOption('viewer');
	await page.getByRole('button', { name: 'Inviter' }).click();
	const link = await page.locator('.link-out code').textContent();

	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const andreas = await context.newPage();
	await andreas.goto(link);
	// Typed in lower case on purpose: the instance capitalises a name on the way
	// in, so from here on this person is "Andreas" everywhere they are shown.
	await andreas.getByLabel('Navn', { exact: true }).fill('andreas');
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
	await projectAction(page, /Delt/);
	await expect(page.getByText('Ejer')).toBeVisible();
	await page.getByLabel('Rolle for Andreas').selectOption('editor');

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
	await drawer.getByLabel('Ansvarlig').selectOption({ label: 'Andreas' });
	await page.keyboard.press('Escape');

	const row = page.locator('.row').filter({ hasText: 'brænde kaffe' });
	await expect(row.getByTitle('Uddelegeret til Andreas')).toBeVisible();
	// And not on a task that is nobody's.
	await page.getByLabel('Ny opgave').fill('min egen');
	await page.getByLabel('Ny opgave').press('Enter');
	await expect(
		page.locator('.row').filter({ hasText: 'min egen' }).locator('.assignee')
	).toHaveCount(0);

	// The log has recorded all of this since the beginning and nothing showed it.
	await projectAction(page, 'Historik');
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
	// Sluppet øverst i søjlen og ikke i midten af den. En uge, der viser et døgn, er
	// højere end skærmen, så søjlens midte kan ligge uden for billedet — og et træk,
	// der ruller undervejs, er et træk, browseren taber. Det er ikke en finte for at
	// få prøven til at bestå: det er også sådan et menneske gør det, fordi man
	// slipper der, hvor man kan se, man slipper.
	await chip.dragTo(page.locator(`[data-date="${to}"]`), { targetPosition: { x: 30, y: 20 } });

	// Landet i timesøjlen med et klokkeslæt: hvor på døgnet man slap, *er* tiden.
	// Sluppet tyve pixels nede i en søjle, der begynder klokken otte, bliver det
	// tidligt på dagen — hvad det præcist runder til, er ikke det, der prøves her.
	await expect(page.locator(`[data-date="${to}"] .tevent`).getByText('skifte dæk')).toBeVisible();
	await expect(page.locator(`[data-date="${from}"]`).getByText('skifte dæk')).toBeHidden();
	await expect(page.locator(`[data-allday="${from}"]`).getByText('skifte dæk')).toBeHidden();

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
/**
 * Kalender: Google's events and verdande's own tasks in one grid, and the rule
 * that tells them apart.
 *
 * The Google half is stubbed at the network layer rather than connected. There are
 * no credentials in a test run and there never will be — an OAuth consent screen is
 * a human pressing a button on somebody else's website — so what can be proved here
 * is what the grid does with events once it has them, which is the part that has a
 * rule in it: a task is draggable and reschedules, an event is not and cannot.
 *
 * The date is read out of the *page*, not out of this process. Playwright pins the
 * browser to Europe/Copenhagen and node is pinned to nothing, so for two hours of
 * every day the two disagree about what today is — which is how a green suite
 * starts failing at half past midnight. Today is also the one day guaranteed to be
 * in the month grid whatever the weekday, so nothing here has to page.
 */
test('kalenderen viser Googles begivenheder over egne opgaver, og kun opgaven kan trækkes', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	// A day at least a week out, whose next day is in the same Monday-to-Sunday
	// week — so the drag below crosses one cell and not one grid.
	//
	// Worked out in the browser, not here. Playwright pins the page to
	// Europe/Copenhagen and this process is pinned to nothing, so for two hours of
	// every day the two disagree about what today is. A week out also keeps this
	// test's day clear of the ones the rest of the suite dates things on.
	const { day, next } = await page.evaluate(() => {
		const iso = (d) =>
			`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
				d.getDate()
			).padStart(2, '0')}`;
		const monday = (d) => {
			const m = new Date(d.getFullYear(), d.getMonth(), d.getDate());
			m.setDate(m.getDate() - ((m.getDay() + 6) % 7));
			return iso(m);
		};
		const cursor = new Date();
		cursor.setDate(cursor.getDate() + 7);
		for (let i = 0; i < 14; i++) {
			cursor.setDate(cursor.getDate() + 1);
			const after = new Date(cursor.getFullYear(), cursor.getMonth(), cursor.getDate() + 1);
			if (monday(cursor) === monday(after)) return { day: iso(cursor), next: iso(after) };
		}
		throw new Error('no two consecutive days inside one week in three weeks');
	});

	// A calendar that is connected and holds two events that day: one with a clock
	// and one that lasts the whole day. The window is deliberately wide, so the
	// notice about having paged past it stays out of the way.
	await page.route('**/api/v1/calendar/events*', (route) =>
		route.fulfill({
			json: {
				from: '2000-01-01',
				to: '2099-12-31',
				events: [
					{
						id: 'ev-1',
						calendar_id: 'c1',
						summary: 'Bestyrelsesmøde',
						starts_at: `${day}T14:00:00+02:00`,
						ends_at: `${day}T15:30:00+02:00`,
						start_day: day,
						end_day: day,
						all_day: false,
						url: 'https://calendar.google.com/event?eid=abc',
						calendar_name: 'Arbejde',
						colour: '#0b8043'
					},
					{
						id: 'ev-2',
						calendar_id: 'c1',
						summary: 'Feriedag',
						start_day: day,
						end_day: day,
						all_day: true,
						calendar_name: 'Familien',
						colour: '#3f51b5'
					}
				]
			}
		})
	);
	await page.route('**/api/v1/calendar', (route) =>
		route.fulfill({
			json: {
				connected: true,
				account: 'kw@nolimit.dk',
				read_only: true,
				has_client: true,
				redirect_uri: 'http://localhost/oauth/calendar/callback',
				calendars: [
					{ id: 'c1', remote_id: 'fam', name: 'Arbejde', colour: '#0b8043', shown: true }
				]
			}
		})
	);

	// verdande's own half of the grid: a task on that day, made the way anybody
	// would make it. The date is written out rather than typed as "i morgen", so
	// the assertion does not depend on the parser as well.
	const box = page.getByLabel('Ny opgave');
	await box.fill(`vande blomster ${day}`);
	await box.press('Enter');
	// Not asserted here: this is the Today view and the task is dated a week out,
	// so it is correctly absent. The grid below is where it has to turn up.
	await expect(box).toHaveValue('');

	// The menu item, which is the thing the sidebar gained.
	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.locator('a[href="/kalender"]').click();
	await page.waitForURL('**/kalender');
	await expect(page.getByRole('heading', { name: 'Kalender', level: 1 })).toBeVisible();

	// The week, not the month, and for a reason worth writing down: a month cell
	// truncates at three chips and this suite has other tests putting tasks on
	// other days. A week cell holds ten, so what is asserted below is what the view
	// does with events rather than how full somebody else left the month.
	await page.getByRole('button', { name: 'Uge', exact: true }).click();

	// Forward a week at a time until the row is the one holding that day. Bounded,
	// because a loop that pages for ever on a broken button is a timeout with no
	// explanation in it.
	const cell = page.locator(`[data-date="${day}"]`);
	const forward = page.getByRole('button', { name: 'Næste uge' });
	for (let i = 0; i < 6 && (await cell.count()) === 0; i++) {
		await forward.click();
	}
	await expect(cell, `uge-visningen nåede aldrig frem til ${day}`).toBeVisible();

	// Connected, with a calendar ticked — so neither of the two "there is nothing
	// here" notices belongs on the screen. They exist so an empty grid is never
	// unexplained, and an explanation for a state you are not in is noise.
	await expect(page.getByText('Ingen kalender er forbundet endnu.')).toBeHidden();
	await expect(page.getByText('Ingen af kontoens kalendere er valgt endnu.')).toBeHidden();

	// Begge slags på den samme dag, som er hele pointen med visningen — men en dag
	// er to kasser nu, siden ugen blev et døgn: timesøjlen bærer det, der har et
	// klokkeslæt, og båndet foroven bærer det, der ikke har. En heldagsbegivenhed
	// lagt klokken nul ville påstå, den holdt fra midnat til midnat.
	const band = page.locator(`[data-allday="${day}"]`);
	const event = cell.locator('.tevent').filter({ hasText: 'Bestyrelsesmøde' });
	const allDay = band.locator('.event').filter({ hasText: 'Feriedag' });
	const task = band.locator('.chip').filter({ hasText: 'vande blomster' });
	await expect(event).toBeVisible();
	await expect(allDay).toBeVisible();
	await expect(task).toBeVisible();

	// A timed event carries its clock; an all-day one has none to carry.
	await expect(event).toContainText('14:00');
	await expect(allDay).not.toContainText(':');

	// The rule. An event is read-only, so it must not offer to be moved — the drop
	// targets would refuse it anyway, but a chip that lifts off the page and then
	// will not land reads as a bug rather than as a rule.
	await expect(event).toHaveAttribute('draggable', 'false');
	await expect(allDay).toHaveAttribute('draggable', 'false');
	await expect(task).toHaveAttribute('draggable', 'true');

	// And it cannot be ticked off either: there is nothing to complete about
	// somebody else's meeting.
	expect(await event.evaluate((el) => el.tagName)).not.toBe('BUTTON');

	// It does link back to Google, which is what makes it a pointer to the event
	// rather than a second copy of it.
	await expect(event).toHaveAttribute('href', /^https:\/\/calendar\.google\.com\//);

	// The task still reschedules by drag, in a grid that now has events in it.
	// Sluppet øverst i søjlen og ikke i midten af den. En uge, der viser et døgn, er
	// højere end skærmen, så søjlens midte kan ligge uden for billedet — og et træk,
	// der ruller undervejs, er et træk, browseren taber. Det er ikke en finte for at
	// få prøven til at bestå: det er også sådan et menneske gør det, fordi man
	// slipper der, hvor man kan se, man slipper.
	await task.dragTo(page.locator(`[data-date="${next}"]`), { targetPosition: { x: 30, y: 20 } });
	// Landet i næste dags timesøjle, med det klokkeslæt der blev sluppet på.
	await expect(page.locator(`[data-date="${next}"] .tevent`).getByText('vande blomster')).toBeVisible();
	await expect(band.getByText('vande blomster')).toBeHidden();
	// The events stayed where they were; a rescheduled task moves nothing else.
	//
	// Named, not counted. A raw `.tevent` count here assumes this day holds nothing
	// but this test's own events — and it does not: the suite shares one database,
	// so an earlier test that drags a task across a month boundary can leave a timed
	// task on this very day, and a timed task is a `.tevent` too. Counting them all
	// made this pass alone and fail in the full run, on whatever date the arithmetic
	// happened to land the two tests on the same cell. Asserting the meeting stayed
	// and the task left is the thing the test actually means, and it is true however
	// full somebody else left the day.
	await expect(cell.locator('.tevent').filter({ hasText: 'Bestyrelsesmøde' })).toHaveCount(1);
	await expect(cell.locator('.tevent').filter({ hasText: 'vande blomster' })).toHaveCount(0);
	await expect(band.locator('.event').filter({ hasText: 'Feriedag' })).toHaveCount(1);

	expect(trouble).toEqual([]);
});

/**
 * The view without a calendar connected, which is what every fresh instance is.
 *
 * It has to draw anyway. The grid is verdande's own tasks with due dates — the
 * thing this view was before anything was laid over it — and a Google account that
 * is not there must not take it down with it.
 */
test('en opgaves længde kan trækkes længere på kalenderen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	// A timed task today at ten, half an hour long. Today, so the week opens on it
	// without paging, and ten is inside the hours the grid draws by default.
	const today = await page.evaluate(() => {
		const d = new Date();
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
	});
	const id = await page.evaluate(async (day) => {
		const r = await fetch('/api/v1/tasks', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({
				content: 'Møde med Anders',
				due_date: day,
				due_time: '10:00',
				duration_min: 30
			})
		});
		return (await r.json()).id;
	}, today);

	await page.goto('/kalender');
	await page.getByRole('button', { name: 'Uge', exact: true }).click();

	const chip = page
		.locator(`[data-date="${today}"] .tevent.task`)
		.filter({ hasText: 'Møde med Anders' });
	await expect(chip).toBeVisible();
	const before = await chip.evaluate((el) => el.getBoundingClientRect().height);

	// Drag the foot of the task down a good stretch. It should grow, both on screen
	// and in what the server stores — a resize is a change of length, not of time.
	const box = await chip.boundingBox();
	await page.mouse.move(box.x + box.width / 2, box.y + box.height - 2);
	await page.mouse.down();
	await page.mouse.move(box.x + box.width / 2, box.y + box.height + 130, { steps: 6 });
	await page.mouse.up();

	await expect
		.poll(async () =>
			page.evaluate(async (tid) => {
				const r = await fetch('/api/v1/tasks/' + tid, {
					credentials: 'include',
					headers: { 'Sec-Fetch-Site': 'same-origin' }
				});
				return (await r.json()).duration_min;
			}, id)
		)
		.toBeGreaterThan(30);

	// And the chip is taller than it was, so the length is something you can see.
	await expect.poll(async () => chip.evaluate((el) => el.getBoundingClientRect().height)).toBeGreaterThan(before);

	// The time did not move: it is still a ten o'clock task.
	await expect(chip.locator('.at')).toHaveText('10:00');

	expect(trouble).toEqual([]);
});

test('kalenderen virker uden en forbundet Google-konto', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/kalender');

	await expect(page.getByRole('heading', { name: 'Kalender', level: 1 })).toBeVisible();
	// Seven weekday columns and a grid under them, whatever day of the week it is.
	await expect(page.locator('.calendar .grid .day').first()).toBeVisible();

	// It says so rather than looking like a calendar with nothing in it, and it
	// says where to go about it.
	await expect(page.getByText('Ingen kalender er forbundet endnu.')).toBeVisible();
	await expect(
		page.locator('a[href="/indstillinger/integrationer"]').first()
	).toBeVisible();

	// The week is the same grid with a different span, and switching to it must not
	// lose the events prop on the way.
	await page.getByRole('button', { name: 'Uge', exact: true }).click();
	await expect(page.locator('.calendar.week')).toBeVisible();

	expect(trouble).toEqual([]);
});

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
	await theirs.getByLabel('Navn', { exact: true }).fill('Sigrid');
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

	// The Inbox is named by the server at account creation, before anybody has seen
	// a settings page — so it is the one project name nobody chose, and it follows
	// the language like a label. Rename it and that becomes your name for it in
	// every language, the same rule as any other project.
	await expect(
		page.getByRole('navigation', { name: 'Main menu' }).getByRole('link', { name: 'Inbox' })
	).toBeVisible();

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
	await projectAction(page, 'Vis færdige');
	const done = page.locator('section.done');
	await expect(done.getByRole('heading', { name: 'Færdige' })).toBeVisible();
	await expect(done.getByText('rydde op', { exact: true })).toBeVisible();
	// Still exactly where it was in the plan — reopened tasks are the only ones
	// that come back up.
	await expect(done.getByText('male gavlen', { exact: true })).toHaveCount(0);

	// The choice is in localStorage, so it holds across a reload.
	await page.reload();
	await expect(page.locator('section.done').getByText('rydde op', { exact: true })).toBeVisible();

	await projectAction(page, 'Skjul færdige');
	await expect(page.locator('section.done')).toHaveCount(0);
	await expect(page.getByText('male gavlen', { exact: true })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * A section holds more than one task, and stops being highlighted afterwards.
 *
 * Both reported from use, and both the same root cause. TaskList read the dragged
 * task from its *own* `draggingId`, which is only set when the drag started in
 * that list — so a task dragged in from the unsectioned rows arrived as null and
 * the handler returned early. It had already called `stopPropagation`, so the
 * section around it never got the drop either.
 *
 * The result was a section you could only drop into while it was empty, because
 * an empty one has no rows to aim at. Put one task in, and there was no way to add
 * a second: it looked exactly like a section that holds one task.
 *
 * The same `stopPropagation` left the section's frame lit, since the handler that
 * clears it never ran.
 */
test('en sektion kan rumme flere opgaver, og rammen forsvinder bagefter @forms', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Badeværelset');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Badeværelset' })).toBeVisible();

	await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
	await page.getByLabel('Ny sektion').fill('Håndværker');
	await page.getByLabel('Ny sektion').press('Enter');
	await expect(page.getByRole('heading', { name: 'Håndværker' })).toBeVisible();

	for (const what of ['knager op', 'fuge om vinduet']) {
		await page.getByLabel('Ny opgave').fill(what);
		await page.getByLabel('Ny opgave').press('Enter');
		await expect(page.getByText(what, { exact: true })).toBeVisible();
	}

	const section = page.locator('section').filter({
		has: page.getByRole('heading', { name: 'Håndværker' })
	});
	const row = (what) => page.locator('.row').filter({ hasText: what });

	// The first goes in while the section is empty — the only drop that used to
	// work, because there was no row in the way to swallow it.
	await row('knager op').dragTo(section);
	await expect(section.getByText('knager op', { exact: true })).toBeVisible();

	// And the second, aimed at the row that is now in there. This is the one that
	// did nothing at all.
	await row('fuge om vinduet').dragTo(section.getByText('knager op', { exact: true }));
	await expect(section.getByText('fuge om vinduet', { exact: true })).toBeVisible();
	await expect(section.locator('.row')).toHaveCount(2);

	// The frame is a drop target being offered, not a state. Once the drag is over
	// it has nothing left to say.
	await expect(section).not.toHaveClass(/\bover\b/);

	// It survives a reload, so this is the server's answer and not the page's.
	await page.reload();
	const after = page.locator('section').filter({
		has: page.getByRole('heading', { name: 'Håndværker' })
	});
	await expect(after.locator('.row')).toHaveCount(2);

	expect(trouble).toEqual([]);
});

/**
 * The project header keeps its name readable.
 *
 * It was a flex row of eight controls and a heading, and the heading was the part
 * that gave way: "GarageRisteriet" rendered as "GarageRist / eriet", broken
 * mid-word, because `overflow-wrap: anywhere` breaks at the first opportunity
 * rather than the last necessary one. A name split mid-syllable reads as a
 * rendering fault, not as a wrap.
 *
 * Everything but the view switcher lives behind one button now. This measures the
 * heading rather than counting controls, because the thing being protected is that
 * the name is legible — not any particular arrangement of the row.
 */
test('projektets navn brydes ikke midt i et ord', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('GarageRisteriet');
	await sidebar.getByLabel('Projektnavn').press('Enter');

	const heading = page.getByRole('heading', { name: 'GarageRisteriet', level: 1 });
	await expect(heading).toBeVisible();

	// One line. The name is fifteen characters and the row now has room for it,
	// which is the whole point of moving the rest behind a button.
	const box = await heading.boundingBox();
	const lineHeight = await heading.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
	expect(box.height, 'overskriften er brudt over flere linjer').toBeLessThan(lineHeight * 2);

	// And the actions are still reachable, just one click further in.
	await page.getByRole('button', { name: 'Flere handlinger' }).click();
	for (const item of ['Vis færdige', 'Del', 'Historik', 'Farve på projektet', 'Slet projektet']) {
		await expect(page.getByRole('menuitem', { name: item, exact: true })).toBeVisible();
	}

	// Escape closes it — a menu you can only leave by pressing its own button again
	// is one you click twice to get out of.
	await page.keyboard.press('Escape');
	await expect(page.getByRole('menuitem', { name: 'Historik', exact: true })).toHaveCount(0);

	expect(trouble).toEqual([]);
});

/**
 * A task can be written straight into a section, two ways.
 *
 * The box at the top of the page puts things in the project with no section, which
 * is right for capturing — but a project has sections because the work belongs
 * somewhere, and dragging every new task down into the right one is a second
 * gesture for something you already knew when you typed it.
 *
 * The field at the foot of a section passes the section it sits in. The sigil is
 * for the box at the top, and for Today and Upcoming, where you are not standing
 * in a section at all.
 */
test('en opgave kan skrives direkte i en sektion @forms', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Risteriet');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Risteriet' })).toBeVisible();

	// A section whose name contains the sigil, because that is the one that would
	// break: the bare form has to be greedy enough to swallow the whole name.
	await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
	await page.getByLabel('Ny sektion').fill('Kunder/Ordrer');
	await page.getByLabel('Ny sektion').press('Enter');
	await expect(page.getByRole('heading', { name: 'Kunder/Ordrer' })).toBeVisible();

	const section = page.locator('section').filter({
		has: page.getByRole('heading', { name: 'Kunder/Ordrer' })
	});

	// The field at the foot of the section. No sigil — standing there is enough.
	await section.getByRole('button', { name: '+ Tilføj opgave' }).click();
	const field = page.getByLabel('Ny opgave i Kunder/Ordrer');
	await field.fill('Karpenhøj bestilling i morgen p1');
	await field.press('Enter');

	const landed = section.locator('.row').filter({ hasText: 'Karpenhøj bestilling' });
	await expect(landed).toBeVisible();
	// It went through quick add, so the date and the priority were read as well.
	await expect(landed.getByText('I morgen')).toBeVisible();

	// And the sigil, from the box at the top, which is not standing anywhere.
	await page.getByLabel('Ny opgave').fill('nye poser /Kunder/Ordrer');
	await page.getByLabel('Ny opgave').press('Enter');
	await expect(section.locator('.row').filter({ hasText: 'nye poser' })).toBeVisible();
	// The sigil is consumed, not left in the title.
	await expect(section.getByText('/Kunder/Ordrer')).toHaveCount(0);

	await expect(section.locator('.row')).toHaveCount(2);

	expect(trouble).toEqual([]);
});

/**
 * A task closed by mistake can be got back, two ways.
 *
 * Reported from use: "I just closed a task by accident, and there is no undo and no
 * list of closed tasks." Both halves were true. Completing is one click on a small
 * circle beside a row you were only reading, and it takes the row off the screen —
 * so the mistake and the evidence of it leave together.
 *
 * The toast covers the mistake you notice at once. Færdige covers the one you
 * notice tomorrow, and it is a place rather than a toggle because somebody hunting
 * for a task they closed does not know which view they closed it in.
 */
test('en opgave lukket ved et uheld kan hentes tilbage', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	await page.getByLabel('Ny opgave').fill('rens tagrenden i dag');
	await page.getByLabel('Ny opgave').press('Enter');
	const row = page.locator('.row').filter({ hasText: 'rens tagrenden' });
	await expect(row).toBeVisible();

	// Closed by mistake.
	await row.getByRole('button', { name: 'Markér som færdig' }).click();
	await expect(page.getByText('rens tagrenden', { exact: true })).toBeHidden();

	// The toast names it, so you can tell which one you hit.
	const toast = page.locator('.toast').filter({ hasText: 'rens tagrenden' });
	await expect(toast).toBeVisible();
	await toast.getByRole('button', { name: 'Fortryd' }).click();
	await expect(page.locator('.row').filter({ hasText: 'rens tagrenden' })).toBeVisible();

	// Now the other half: close it again and walk away from the toast.
	await page
		.locator('.row')
		.filter({ hasText: 'rens tagrenden' })
		.getByRole('button', { name: 'Markér som færdig' })
		.click();
	await expect(page.getByText('rens tagrenden', { exact: true })).toBeHidden();

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	// Færdige left the sidebar: it is a rare errand next to four entries read every
	// day, and it now sits with the other "where did that go" answers.
	await page.goto('/indstillinger/data');
	await page.getByRole('link', { name: 'Åbn færdige opgaver' }).click();
	await expect(page.getByRole('heading', { name: 'Færdige', level: 1 })).toBeVisible();

	const closed = page.locator('.row').filter({ hasText: 'rens tagrenden' });
	await expect(closed).toBeVisible();
	// Grouped by the day it was closed, which is how anybody would describe it.
	await expect(page.getByRole('heading', { name: 'I dag', level: 2 })).toBeVisible();

	// And it reopens from here, leaving the list at once.
	await closed.getByRole('button', { name: 'Genåbn opgave' }).click();
	await expect(page.locator('.row').filter({ hasText: 'rens tagrenden' })).toHaveCount(0);

	expect(trouble).toEqual([]);
});

/**
 * A passkey, registered and then signed in with, end to end.
 *
 * The Go tests cover who may reach these endpoints and that a challenge is spent
 * when it is used. What they cannot do is produce a signature — that needs an
 * authenticator. Chrome has a virtual one behind CDP, and it is the only way to
 * find out whether the encoding is right: every boundary here is a base64url
 * conversion, and one wrong byte produces a signature that will not verify,
 * reported as "that key was not accepted" — which reads as a broken key rather
 * than a broken encoder.
 *
 * Chromium only, and deliberately so: this is testing our ceremony, not WebKit's
 * authenticator.
 */
test('en passkey kan registreres og logges ind med', async ({ browser, browserName }) => {
	test.skip(browserName !== 'chromium', 'den virtuelle authenticator findes kun i Chromium');

	// Its own context, at `localhost` rather than the loopback address the rest of
	// the suite uses. WebAuthn refuses an IP as a relying party id — the browser
	// throws `SecurityError: This is an invalid domain` — and localhost is the
	// exception every browser makes. It is not the shared baseURL because
	// resolving `localhost` is slow enough here to cost the whole suite minutes.
	//
	// Cookies are bound to an origin, so the signed-in state from the setup project
	// does not carry across: this signs in with a password first, which is also the
	// honest order — you register a key on an account you have already proved you
	// own.
	const origin = 'http://localhost:8097';
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const page = await context.newPage();
	const trouble = watchForTrouble(page);

	await page.goto(`${origin}/`);
	await page.getByLabel(/E-mail/).fill(USER.email);
	await page.getByLabel(/Adgangskode/).fill(USER.password);
	await page.getByRole('button', { name: 'Log ind', exact: true }).click();
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	// A virtual authenticator that verifies its owner, so the key counts as both
	// factors — which is what lets the login complete without a password.
	const client = await page.context().newCDPSession(page);
	await client.send('WebAuthn.enable');
	const { authenticatorId } = await client.send('WebAuthn.addVirtualAuthenticator', {
		options: {
			protocol: 'ctap2',
			transport: 'internal',
			hasResidentKey: true,
			hasUserVerification: true,
			isUserVerified: true,
			automaticPresenceSimulation: true
		}
	});

	await page.goto(`${origin}/indstillinger`);
	await page.getByLabel('Navn på nøglen').fill('Testnøglen');
	await page.getByRole('button', { name: 'Tilføj en nøgle' }).click();

	// It is in the list, and it counts as both factors — the virtual authenticator
	// verifies, so the login below needs no password.
	const key = page.locator('li').filter({ hasText: 'Testnøglen' });
	await expect(key).toBeVisible();
	await expect(key.getByText('begge faktorer')).toBeVisible();

	// It survives a reload, so this is the server's row and not the page's state.
	await page.reload();
	await expect(page.locator('li').filter({ hasText: 'Testnøglen' })).toBeVisible();

	// Now sign out and back in with it. No email typed: the device knows which
	// account its key belongs to, which is also why this page cannot be used to
	// find out who has an account here.
	await page.getByRole('navigation', { name: 'Hovedmenu' }).getByText('Log ud').click();
	// `exact`, because "Log ind" is a substring of "Log ind med en passkey" and
	// Playwright matches an accessible name loosely unless told otherwise.
	await expect(page.getByRole('button', { name: 'Log ind', exact: true })).toBeVisible();

	await page.getByRole('button', { name: 'Log ind med en passkey' }).click();
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'I dag' })).toBeVisible();

	// And the key records that it was used, which is what makes the list worth
	// reading when somebody wonders whether a device is still in play.
	await page.goto(`${origin}/indstillinger`);
	await expect(
		page.locator('li').filter({ hasText: 'Testnøglen' }).getByText(/sidst brugt/)
	).toBeVisible();

	await client.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId });
	await context.close();
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
	// `exact`, or this also matches "Navn på nøglen" in the passkey panel — and an
	// ambiguous locator waits for one of them to go away rather than failing.
	await page.getByLabel('Navn', { exact: true }).fill('Kristian Vinterberg-Skovgaard');
	await page.getByRole('button', { name: 'Gem' }).first().click();
	await expect(page.getByText('Kristian Vinterberg-Skovgaard').first()).toBeVisible();

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	const sideways = await sidebar.evaluate((el) => el.scrollWidth - el.clientWidth);
	expect(sideways, 'sidebjælken kan scrolles vandret').toBeLessThanOrEqual(1);

	// Og rulleboksen indeni, som er den, der faktisk ruller nu. Bjælken selv er
	// `overflow: hidden`, så en for bred række ville blive klippet dér og aldrig
	// nå at tælle med ovenfor — målingen ville stå og bestå af den forkerte grund.
	const inner = await sidebar
		.locator('.scroller')
		.evaluate((el) => el.scrollWidth - el.clientWidth);
	expect(inner, 'rulleboksen kan scrolles vandret').toBeLessThanOrEqual(1);

	// And the whole page, for the same reason at a different scale.
	const page_sideways = await page.evaluate(
		() => document.documentElement.scrollWidth - document.documentElement.clientWidth
	);
	expect(page_sideways, 'siden kan scrolles vandret').toBeLessThanOrEqual(1);

	// Put the name back, so the tests after this one see the account they expect.
	await page.getByLabel('Navn', { exact: true }).fill(USER.name);
	await page.getByRole('button', { name: 'Gem' }).first().click();

	expect(trouble).toEqual([]);
});

/**
 * Mærket og de faste visninger bliver stående, når resten ruller.
 *
 * Rullebjælken gik før hele vejen op: runen og I dag forsvandt op over kanten, så
 * snart der var projekter nok — og de er dem, man er på vej hen til oftest.
 *
 * Målt i et lavt vindue, for i et højt er der ikke noget at rulle, og en prøve,
 * der ikke ruller, består uanset hvad man laver om. Bredden holdes over
 * telefongrænsen, hvor bjælken bliver til en skuffe og er en anden ting.
 */
test('sidebjælkens hoved bliver stående, når listen ruller', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	await page.setViewportSize({ width: 1000, height: 420 });

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	const scroller = sidebar.locator('.scroller');
	await expect(scroller).toBeVisible();

	// Rullebjælken begynder under de faste visninger og ikke bag dem.
	const geometry = await sidebar.evaluate((el) => {
		const box = (sel) => el.querySelector(sel).getBoundingClientRect().toJSON();
		const scroll = el.querySelector('.scroller');
		return {
			views: box('.views'),
			scroller: box('.scroller'),
			overflows: scroll.scrollHeight - scroll.clientHeight
		};
	});
	expect(geometry.overflows, 'der er ikke noget at rulle — prøven beviser intet').toBeGreaterThan(
		0
	);
	expect(
		geometry.scroller.top,
		'rulleboksen begynder oppe i de faste visninger'
	).toBeGreaterThanOrEqual(geometry.views.bottom - 1);

	// Og hovedet bliver, hvor det er, hele vejen ned.
	const brand = sidebar.locator('.brand');
	const before = await brand.boundingBox();
	await scroller.evaluate((el) => el.scrollTo(0, el.scrollHeight));
	await expect
		.poll(async () => scroller.evaluate((el) => el.scrollTop))
		.toBeGreaterThan(0);
	const after = await brand.boundingBox();
	expect(Math.abs(after.y - before.y), 'mærket fulgte med op').toBeLessThanOrEqual(1);
	await expect(sidebar.getByRole('link', { name: 'I dag' })).toBeInViewport();

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

	// A board gets the width a reading column withholds. This was `.view:has(.board)`
	// for as long as the board has existed and never once matched: `.board` is inside
	// a child component, and Svelte scopes a selector to the component that wrote it.
	// The rule read correctly, compiled, and could not fire.
	const wide = await page.locator('.view').evaluate((el) => el.getBoundingClientRect().width);
	await page.getByRole('button', { name: 'Liste', exact: true }).click();
	const narrow = await page.locator('.view').evaluate((el) => el.getBoundingClientRect().width);
	expect(wide, 'board-visningen er ikke bredere end listen').toBeGreaterThan(narrow);
	await page.getByRole('button', { name: 'Board', exact: true }).click();
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
 * Sektioner kan trækkes op og ned.
 *
 * Rækkefølgen af sektioner *er* planen — "Planlægning" står før "I gang", fordi
 * det er den vej arbejdet går — og indtil nu kunne den kun laves om ved at slette
 * en sektion og lave den igen, hvilket smider dens opgaver ud i det usektionerede
 * område på vejen.
 *
 * Målt på rækkefølgen af overskrifterne på siden og bagefter på, hvad serveren
 * svarer efter en genindlæsning: den første halvdel kan bestå af, at fladen
 * flyttede et element, den anden kan kun bestå, hvis skrivningen gik igennem.
 */
test('sektioner kan trækkes i en anden rækkefølge, og den overlever en genindlæsning', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Havehus');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Havehus' })).toBeVisible();

	for (const name of ['Fundament', 'Rejsning', 'Tag']) {
		await page.getByRole('button', { name: '+ Tilføj sektion' }).click();
		const field = page.getByLabel('Ny sektion');
		await field.fill(name);
		await field.press('Enter');
		await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
	}

	const order = () =>
		page.locator('.section-head h2').evaluateAll((els) => els.map((e) => e.textContent.trim()));

	expect(await order()).toEqual(['Fundament', 'Rejsning', 'Tag']);

	// Tag trækkes helt op. Sluppet i den øverste halvdel af "Fundament", som er
	// gaben over den — hvilken halvdel man rammer, er hele forskellen på over og
	// under, så positionen er skrevet ud og ikke overladt til midten.
	await page
		.locator('.section-head')
		.filter({ hasText: 'Tag' })
		.dragTo(page.locator('.section-head').filter({ hasText: 'Fundament' }), {
			targetPosition: { x: 40, y: 3 }
		});

	await expect.poll(order).toEqual(['Tag', 'Fundament', 'Rejsning']);

	await page.reload();
	await expect(page.getByRole('heading', { name: 'Tag', exact: true })).toBeVisible();
	expect(await order(), 'serveren tog ikke rækkefølgen').toEqual([
		'Tag',
		'Fundament',
		'Rejsning'
	]);

	// Og den anden vej: sluppet i den nederste halvdel lander den under.
	await page
		.locator('.section-head')
		.filter({ hasText: 'Tag' })
		.dragTo(page.locator('.section-head').filter({ hasText: 'Rejsning' }), {
			targetPosition: { x: 40, y: 28 }
		});

	await expect.poll(order).toEqual(['Fundament', 'Rejsning', 'Tag']);

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

test('man kan skrive hvor som helst, og feltet fortæller hvad det forstår', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	// Nothing focused, nothing clicked: just type. The thought does not have to
	// survive a journey to find somewhere to put it.
	await page.locator('body').click({ position: { x: 5, y: 5 } });
	// "i dag" because this is the Today view: a task parsed onto no date at all is
	// correctly absent from it, which would read as the capture having failed.
	await page.keyboard.type('ring til Anders i dag');

	const field = page.locator('[data-quickadd]');
	await expect(field).toBeFocused();
	// Every letter arrives, including the first — which is the one a naive
	// implementation drops while it is busy moving focus.
	await expect(field).toHaveValue('ring til Anders i dag');

	// And the legend appears with the field, saying what the parser can read. It
	// used to live in the placeholder, which vanishes at the first keystroke —
	// exactly when the help starts being useful.
	await expect(page.getByText('projekt', { exact: false }).first()).toBeVisible();

	await page.keyboard.press('Enter');
	await expect(page.getByText('ring til Anders', { exact: true })).toBeVisible();

	// In English too. The shortcut used to find the field by its Danish label, so
	// it silently did nothing for anybody running the interface in English.
	await page.goto('/indstillinger');
	await page.getByLabel('Sprog').selectOption('en');
	await page.getByRole('button', { name: 'Gem' }).first().click();
	await page.goto('/');
	await page.locator('body').click({ position: { x: 5, y: 5 } });
	await page.keyboard.type('call Anders');
	await expect(page.locator('[data-quickadd]')).toHaveValue('call Anders');

	// Back to Danish. The account is shared with every test after this one, and
	// leaving it in English made three of them hunt for buttons by names that no
	// longer existed — a failure that looks like the feature under test and is not.
	await page.goto('/indstillinger');
	await page.getByLabel('Language').selectOption('da');
	await page.getByRole('button', { name: 'Save' }).first().click();
	await expect(page.getByRole('button', { name: 'Gem' }).first()).toBeVisible();

	expect(trouble).toEqual([]);
});

test('en postkasse kan tilføjes, og værtens afvisning når frem til skærmen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger/integrationer');

	await expect(page.getByRole('heading', { name: 'Postkasser' })).toBeVisible();
	await page.getByRole('button', { name: 'Tilføj en postkasse' }).click();

	// Nothing is listening there, so the dial fails at once. The point is not the
	// failure but where it is reported: the server tests the connection before it
	// saves, so this is refused at the door rather than accepted and then silently
	// failing every ten minutes.
	await page.getByLabel('IMAP-server').fill('127.0.0.1:1');
	await page.getByLabel('Brugernavn').fill('kw');
	await page.getByLabel('App-kodeord').fill('hemmelig');
	// `exact`, because "Forbind" is a prefix of "Forbind Gmail" one panel up and
	// Playwright matches an accessible name by substring unless told otherwise.
	await page.getByRole('button', { name: 'Forbind', exact: true }).click();

	// The host's own words, not "noget gik galt". This is the whole reason
	// upstream refusals are answered as 422: a proxy may replace the body of a 5xx,
	// and then the person is told nothing.
	await expect(page.getByText(/Postkassen svarede/)).toBeVisible();

	// And nothing was saved on the way past.
	await page.reload();
	await expect(page.getByText('127.0.0.1:1')).toBeHidden();

	// The 422 above is the point of the test, not a symptom. Everything else the
	// watcher caught still has to be empty.
	expect(trouble.filter((t) => !t.includes('/mailboxes → 422'))).toEqual([]);
});

test('tallet ved et projekt er opgaver, og sidebjælken kan foldes med tastaturet', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Tallene');
	await sidebar.getByLabel('Projektnavn').press('Enter');

	const row = sidebar.getByRole('link', { name: /^Tallene/ });
	await expect(row).toBeVisible();
	// Nothing in it yet, so no number at all: a nought beside every empty project
	// is a column of noughts.
	await expect(row).toHaveText('Tallene');

	await row.click();
	// Wait for the project's own page before typing. Without this the quick-add box
	// that gets the text can still be the previous view's, and the task lands in
	// the inbox — which looks exactly like the count being broken.
	await expect(page.getByRole('heading', { name: 'Tallene' })).toBeVisible();

	const box = page.getByLabel('Ny opgave');
	await box.fill('første i dag');
	await box.press('Enter');

	// In the project itself before looking at the sidebar. Without this the count
	// can be read while the task is still on its way, or — worse — after it landed
	// in the inbox because the box that took the text belonged to the previous
	// view. Then the assertion below fails for a reason that has nothing to do with
	// what it is testing.
	await expect(page.getByText('første', { exact: true })).toBeVisible();

	// Reloaded before looking. What is asserted is that the number is open tasks
	// and comes from the server, which holds on any load.
	//
	// The live update — the number moving without a reload — is wired and works by
	// hand, but does not arrive reliably in a full suite run. Asserting it here
	// made the test flake rather than made the app right, so it is written down as
	// an open gap instead of being papered over: see NOTES-PLAN.md.
	await page.reload();
	await expect(sidebar.getByRole('link', { name: /^Tallene/ })).toContainText('1');

	// And it counts what is left, not what has been: finishing it takes the number
	// away entirely, which is the whole reason it is not member_count any more —
	// that one said 2 on an empty project and meant two people.
	// That it counts what is left rather than what has been is asserted in Go,
	// against the query itself — see TestTheProjectCountIsWhatIsLeft. Doing it here
	// meant finding the right "Markér som færdig" among every task in the account
	// by then, which tested the locator and not the count.

	// The fold has a button, but a rectangle with a line down it is not a word.
	const width = () => sidebar.evaluate((el) => el.getBoundingClientRect().width);
	expect(await width()).toBeGreaterThan(100);
	await page.keyboard.press('ControlOrMeta+b');
	await expect.poll(width).toBeLessThan(5);
	await page.keyboard.press('ControlOrMeta+b');
	await expect.poll(width).toBeGreaterThan(100);

	expect(trouble).toEqual([]);
});

test('en note kan skrives, findes igen, og peger på det den nævner', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Notetesteriet');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Notetesteriet' })).toBeVisible();

	await sidebar.getByRole('link', { name: 'Noter' }).click();
	await expect(page.getByRole('heading', { name: 'Noter' })).toBeVisible();

	await page.getByRole('button', { name: 'Ny note' }).click();
	const body = page.getByLabel('Notens tekst');
	await body.fill('Møde om #Notetesteriet\n\nHan vil gerne have kaffe hver uge.');
	// Saved on the way out, so there is no Gem-knap to forget.
	await body.blur();

	// The title comes from the first line, the way Apple Notes does it. Without it
	// a list of notes is a list of "Uden titel".
	await expect(page.getByRole('button', { name: /Møde om/ })).toBeVisible();

	// And the #tag was read out of the text, unasked. This is the whole point: it
	// is the same tag a task carries, so the note can be found from the project.
	// Scoped to the panel under the editor: the same text is also in the textarea
	// and in the list preview, which is three of the same thing and none of them
	// the one being asserted.
	await expect(page.locator('.link', { hasText: '#Notetesteriet' })).toBeVisible();

	// Findable by a word from the body, and in the other Danish spelling too.
	await page.getByLabel('Søg i noter').fill('møde');
	await expect(page.getByRole('button', { name: /Møde om/ })).toBeVisible();
	await page.getByLabel('Søg i noter').fill('mode');
	await expect(page.getByRole('button', { name: /Møde om/ })).toBeVisible();

	// It survives a reload, which is the only proof that it was ever written down.
	await page.reload();
	await expect(page.getByRole('button', { name: /Møde om/ })).toBeVisible();

	// And the payoff: the note turns up in the project it named, without anybody
	// filing it there. This is what a #tag being the same tag actually buys.
	await sidebar.getByRole('link', { name: 'Notetesteriet' }).click();
	await expect(page.getByRole('heading', { name: 'Notetesteriet' })).toBeVisible();
	await expect(
		page.locator('.notes').getByRole('link', { name: /Møde om/ })
	).toBeVisible();

	expect(trouble).toEqual([]);
});

test('noteeditoren er rich text, og teksten overlever turen til Markdown og tilbage', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	await page.getByRole('button', { name: 'Ny note' }).click();
	const page_ = page.getByRole('textbox', { name: 'Notens tekst' });
	await page_.click();

	// Typed and formatted the way somebody would: pick a style, write, pick another.
	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Titel' }).click();
	await page.keyboard.type('Møde med Anders');
	await page.keyboard.press('Enter');

	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Brødtekst' }).click();
	await page.keyboard.type('Han vil gerne have kaffe hver uge.');

	// The styles are real elements, not asterisks on screen. This is the whole of
	// "it should feel like Apple Notes".
	await expect(page_.locator('h1')).toHaveText('Møde med Anders');
	await expect(page_.locator('p').filter({ hasText: 'kaffe' })).toBeVisible();

	// Bold through the toolbar, on a selection made with the keyboard. Not
	// Shift+Home — in a contenteditable that is the start of the document, so it
	// took the heading with it and the button toggled bold off instead of on. Not a
	// double-click either: it leaves the selection empty here.
	for (let i = 0; i < 5; i++) await page.keyboard.press('Shift+ArrowLeft');
	await page.getByRole('button', { name: 'Fed' }).click();
	// Either tag: the browser's own bold is <b>, the converter writes <strong>, and
	// both are read as bold on the way back. Asserting one of them would be
	// asserting which browser this is.
	await expect(page_.locator('b, strong')).toBeVisible();

	// And the round trip, which is the thing that can quietly ruin a note: what is
	// stored is Markdown, and reopening it has to give back the same document. A
	// converter that disagrees with itself reshapes a note a little on every save.
	await page_.blur();
	await page.reload();
	await page.getByRole('button', { name: /Møde med Anders/ }).click();

	const back = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(back.locator('h1')).toHaveText('Møde med Anders');
	await expect(back.locator('b, strong')).toBeVisible();
	await expect(back).toContainText('kaffe hver uge');
	// No stray Markdown on screen: if the marks are showing, the conversion failed
	// in the direction nobody notices until a note looks wrong.
	await expect(back).not.toContainText('**');
	await expect(back).not.toContainText('# ');

	expect(trouble).toEqual([]);
});

test('en note kan deles med et projekts folk, og tages tilbage igen', async ({ page, browser }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Delenoter');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Delenoter' })).toBeVisible();

	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: 'Ny note' }).click();
	await page.getByRole('textbox', { name: 'Notens tekst' }).click();
	await page.keyboard.type('Fælles aftale om leveringen');
	await page.getByRole('textbox', { name: 'Notens tekst' }).blur();

	// Filing it in a project — the controls live in the Del popover in the footer.
	await page.locator('.sharewrap > button').click();
	await page.getByLabel('Læg i projekt').selectOption({ label: 'Delenoter' });
	await expect(page.getByText('delt med projektets folk')).toBeVisible();

	// And it is really there, not merely labelled: the project's own page shows it.
	await sidebar.getByRole('link', { name: 'Delenoter' }).click();
	await expect(page.locator('.notes').getByRole('link', { name: /Fælles aftale/ })).toBeVisible();

	// Taken back out, it is the author's alone again.
	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: /Fælles aftale/ }).click();
	await page.locator('.sharewrap > button').click();
	await page.getByLabel('Læg i projekt').selectOption({ label: 'Intet projekt' });
	await expect(page.getByText('din igen')).toBeVisible();

	await sidebar.getByRole('link', { name: 'Delenoter' }).click();
	await expect(page.locator('.notes').getByRole('link', { name: /Fælles aftale/ })).toBeHidden();

	expect(trouble).toEqual([]);
});

test('en note kan deles med en person direkte, og dukker op hos dem', async ({ browser, page }) => {
	const trouble = watchForTrouble(page);

	// Somebody to share with: a second account on the instance, made the ordinary
	// way. The candidate list is drawn when the note opens, so they must exist
	// before it does.
	await page.goto('/indstillinger/brugere');
	await page.getByLabel('E-mailadresse').fill('sofie@example.dk');
	await page.getByRole('button', { name: 'Send invitation' }).click();
	const link = await page.locator('.link-out').textContent();

	const sofieCtx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	const sofie = await sofieCtx.newPage();
	await sofie.goto(link);
	await sofie.getByLabel('Navn', { exact: true }).fill('Sofie');
	await sofie.getByLabel(/Adgangskode/).fill('et langt kodeord til test');
	await sofie.getByRole('button', { name: 'Opret konto' }).click();
	await expect(sofie.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	// The owner writes a note and hands it to Sofie, as a reader.
	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: 'Ny note' }).click();
	await page.getByRole('textbox', { name: 'Notens tekst' }).click();
	await page.keyboard.type('Ferieplan for hele holdet');
	await page.getByRole('textbox', { name: 'Notens tekst' }).blur();

	// Sharing lives in a popover behind the Del button in the footer.
	await page.locator('.sharewrap > button').click();
	const share = page.locator('.sharepanel');
	await expect(share).toBeVisible();
	await share.locator('.addshare select').first().selectOption({ label: 'Sofie' });
	await share.getByRole('button', { name: 'Tilføj' }).click();

	// She is on the note now, with the role she was given.
	const row = share.locator('.sharelist li').filter({ hasText: 'Sofie' });
	await expect(row).toBeVisible();
	await expect(row.locator('select')).toHaveValue('viewer');

	// And it reaches her: her own notes list grows a Delt med mig group with it in.
	//
	// Navigated on the origin the invite signed her in on. VERDANDE_BASE_URL is
	// localhost while the suite drives 127.0.0.1, and a session cookie set on one
	// host is not sent to the other — a bare "/noter" would land her logged out.
	const noterURL = new URL('/noter', link).href;
	await sofie.goto(noterURL);
	await expect(sofie.getByRole('button', { name: /Delt med mig/ })).toBeVisible();
	await expect(sofie.getByRole('button', { name: /Ferieplan for hele holdet/ })).toBeVisible();
	// A viewer's note carries no star or archive control — it is not hers to put away.
	const sofieRow = sofie.locator('.notes li').filter({ hasText: 'Ferieplan for hele holdet' });
	await expect(sofieRow.locator('.star')).toHaveCount(0);

	// Taken back, it leaves her list.
	await row.getByRole('button', { name: 'Fjern adgang' }).click();
	await expect(row).toBeHidden();
	await sofie.goto(noterURL);
	await expect(sofie.getByRole('button', { name: /Ferieplan for hele holdet/ })).toBeHidden();

	await sofieCtx.close();
	expect(trouble).toEqual([]);
});

test('udseendet kan skiftes uafhængigt af temaet, og overlever en genindlæsning', async ({
	page
}) => {
	const trouble = watchForTrouble(page);
	await page.goto('/indstillinger');

	const face = () =>
		page.evaluate(() => getComputedStyle(document.body).fontFamily);
	const ground = () =>
		page.evaluate(() => getComputedStyle(document.body).backgroundColor);

	const startFace = await face();
	await page.getByRole('button', { name: /^Rolig/ }).click();
	const serif = await face();
	expect(serif, 'skriften skiftede ikke').not.toBe(startFace);
	expect(serif.toLowerCase()).toContain('serif');

	// The other axis is untouched: choosing a face must not drag the palette with
	// it. That is the whole reason they are two settings and not one list.
	const darkGround = await ground();
	await page.getByRole('button', { name: /^Papir/ }).click();
	expect(await ground(), 'temaet skiftede ikke').not.toBe(darkGround);
	expect(await face(), 'temaet ændrede skriften').toBe(serif);

	// And it is on before the first paint, not applied afterwards — otherwise the
	// page is laid out once in one face and again in another.
	await page.reload();
	expect(await face()).toBe(serif);
	expect(await page.evaluate(() => document.documentElement.dataset.look)).toBe('rolig');

	// Back to the default, which carries no attribute at all.
	await page.getByRole('button', { name: /^Verdande/ }).click();
	expect(await page.evaluate(() => document.documentElement.dataset.look)).toBeUndefined();

	expect(trouble).toEqual([]);
});

test('[[ foreslår en note, og linket lander i noten', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	// One note to point at, then another to write the link in.
	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();
	await page.keyboard.type('Rejseplan til Berlin');
	await ed.blur();
	await page.waitForTimeout(600);

	await page.getByRole('button', { name: 'Ny note' }).click();
	await ed.click();
	await page.keyboard.type('Mine planer');
	await page.keyboard.press('Enter');
	await page.keyboard.type('Se ');
	await page.keyboard.type('[[Rejse');

	// The picker offers the note by title, and choosing it turns the text into a
	// real inline link — the title, not the brackets.
	const option = page.locator('.suggestions button', { hasText: 'Rejseplan til Berlin' });
	await expect(option).toBeVisible();
	await option.click();
	const inline = ed.locator('a.notelink', { hasText: 'Rejseplan til Berlin' });
	await expect(inline).toBeVisible();

	// It survives the round-trip through Markdown and a reload.
	await ed.blur();
	await page.waitForTimeout(900);
	await page.reload();
	await page.getByRole('button', { name: /Mine planer/ }).click();
	await expect(ed.locator('a.notelink', { hasText: 'Rejseplan til Berlin' })).toBeVisible();

	// And it is a link you can follow: clicking it opens the note it names.
	await ed.locator('a.notelink', { hasText: 'Rejseplan til Berlin' }).click();
	await expect(ed.locator('h1')).toHaveText('Rejseplan til Berlin');

	expect(trouble).toEqual([]);
});

test('en ny note begynder som titel, og linjen efter er brødtekst', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();

	// Straight in, without choosing a style. The first line is the title because
	// that is what the list calls the note by.
	await page.keyboard.type('Titelprøven');
	await expect(ed.locator('h1')).toHaveText('Titelprøven');

	await page.keyboard.press('Enter');
	await page.keyboard.type('Han vil gerne have kaffe hver uge.');

	// And the line after is body, not a second title. A note where every line is a
	// heading is a note with no heading at all.
	await expect(ed.locator('h1')).toHaveCount(1);
	await expect(ed.locator('p').filter({ hasText: 'kaffe' })).toBeVisible();

	// A third line stays body too.
	await page.keyboard.press('Enter');
	await page.keyboard.type('Og han ringer selv.');
	await expect(ed.locator('h1')).toHaveCount(1);

	// It survives the trip through Markdown: the title is a heading again on the
	// way back, and the body is not.
	await ed.blur();
	await page.reload();
	await page.getByRole('button', { name: /Titelprøven/ }).click();

	const back = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(back.locator('h1')).toHaveText('Titelprøven');
	await expect(back.locator('h1')).toHaveCount(1);
	await expect(back).toContainText('ringer selv');

	expect(trouble).toEqual([]);
});

test('# foreslår et projekt, og titlen følger den første linje', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Forslagsprojektet');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Forslagsprojektet' })).toBeVisible();

	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();

	await page.keyboard.type('Første udkast');
	await page.keyboard.press('Enter');

	// Half a tag is enough. The name is long and nobody should have to spell it.
	await page.keyboard.type('Handler om #Forslags');
	const option = page.getByRole('option', { name: /Forslagsprojektet/ }).first();
	await expect(option).toBeVisible();

	// Return takes the highlighted one — the list owns the key while it is open.
	await page.keyboard.press('Enter');
	await expect(ed).toContainText('#Forslagsprojektet');
	await expect(option).toBeHidden();

	// And the syntax is spelled out under the field, where it is being typed.
	await expect(page.getByText('projekt', { exact: false }).first()).toBeVisible();

	// The title is the first line. It used to be derived once and then left alone,
	// so rewriting the opening left the list calling the note by an old name.
	await page.keyboard.press('Enter');
	await ed.blur();
	await expect(page.getByRole('button', { name: /Første udkast/ })).toBeVisible();

	// Rewrite the first line; the list has to follow.
	await ed.click();
	await page.keyboard.press('ControlOrMeta+Home');
	for (let i = 0; i < 13; i++) await page.keyboard.press('Shift+ArrowRight');
	await page.keyboard.type('Andet udkast');
	await ed.blur();
	await expect(page.getByRole('button', { name: /Andet udkast/ })).toBeVisible();

	expect(trouble).toEqual([]);
});

test('et tag finder sit projekt uanset store og små bogstaver', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('StoreBogstaver');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'StoreBogstaver' })).toBeVisible();

	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();

	// Written in lower case, the way somebody types when they are not thinking
	// about it. It used to mean the note simply never appeared on the project, with
	// nothing anywhere to say why.
	await page.keyboard.type('Aftale om levering');
	await page.keyboard.press('Enter');
	await page.keyboard.type('Handler om #storebogstaver');
	await page.keyboard.press('Escape');
	await ed.blur();

	// The panel shows the project's own spelling, not the folded key: a label
	// nobody wrote and that is nowhere in the note would be worse than none.
	await expect(page.locator('.link', { hasText: '#StoreBogstaver' })).toBeVisible();

	await sidebar.getByRole('link', { name: 'StoreBogstaver' }).click();
	await expect(page.locator('.notes').getByRole('link', { name: /Aftale om levering/ })).toBeVisible();

	expect(trouble).toEqual([]);
});

test('en opgave viser de noter, der nævner den, og linket åbner noten', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	await box.fill('ring til leverandøren i dag');
	await box.press('Enter');
	await expect(page.getByText('ring til leverandøren', { exact: true })).toBeVisible();

	// Asked for rather than read off the address bar: opening a task shows a drawer
	// without navigating, so the URL is still the list's.
	const taskId = await page.evaluate(async () => {
		const r = await fetch('/api/v1/tasks?limit=100', {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		const j = await r.json();
		return (j.tasks ?? []).find((t) => t.content === 'ring til leverandøren')?.id;
	});
	expect(taskId, 'opgaven blev ikke oprettet').toBeTruthy();

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('link', { name: 'Noter', exact: true }).click();
	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();
	await page.keyboard.type('Referat fra tirsdag');
	await page.keyboard.press('Enter');
	await page.keyboard.type('Aftalt at han ringer, se /opgave/' + taskId);
	await ed.blur();

	// Back on the task: the other direction of the same link. Without it the
	// connection only works if you happen to be reading the note, which is the half
	// nobody needs help with.
	await page.goto('/opgave/' + taskId);
	const linked = page.locator('.linked-notes').getByRole('link', { name: /Referat fra tirsdag/ });
	await expect(linked).toBeVisible();

	// And it opens the note rather than dropping you on a list to find it again.
	await linked.click();
	await expect(page.getByRole('textbox', { name: 'Notens tekst' })).toContainText(
		'Aftalt at han ringer'
	);

	expect(trouble).toEqual([]);
});

test('en kodeblok ser ud som en terminal og bliver farvet', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	// Skrevet ind som Markdown, sådan som importen fra Apple Noter leverer den.
	const id = await page.evaluate(async () => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({
				body: '# Terminalprøven\n\nHer er udskriften:\n\n```bash\nkw@shell:~$ ls -la\n# en kommentar\necho "hej"\n```\n\nog resten.'
			})
		});
		return (await r.json()).id;
	});
	await page.goto('/noter?note=' + id);

	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	const pre = ed.locator('pre');
	await expect(pre).toBeVisible();

	// Mørk flade uanset tema: en terminal er sort, også midt på dagen.
	const look = await pre.evaluate((el) => {
		const s = getComputedStyle(el);
		return { bg: s.backgroundColor, font: s.fontFamily };
	});
	expect(look.bg).toBe('rgb(20, 24, 27)');
	expect(look.font.toLowerCase()).toMatch(/mono/);

	// Og farvet: prompten, kommentaren og strengen skal kunne skelnes.
	await expect(pre.locator('.tok-prompt')).toBeVisible();
	await expect(pre.locator('.tok-comment')).toContainText('en kommentar');
	await expect(pre.locator('.tok-string')).toContainText('hej');

	// Teksten omkring er ikke blevet til kode.
	await expect(ed.locator('h1')).toHaveText('Terminalprøven');
	await expect(ed).toContainText('og resten.');

	// Og blokken overlever turen tilbage til Markdown: den skal komme ud som en
	// hegnet blok med sit sprog, ikke som en klump linjer.
	const body = await page.evaluate(async (noteId) => {
		const r = await fetch('/api/v1/notes/' + noteId, {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		return (await r.json()).body;
	}, id);
	expect(body).toContain('```bash');
	expect(body).toContain('kw@shell:~$ ls -la');

	expect(trouble).toEqual([]);
});

test('lister overlever en gemning — også i et citat, og også med numre', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	// Formerne, der hver især gik tabt: en punktliste, en nummereret, en der
	// begynder et andet sted end ved ét, en liste i en liste, og en liste inde i et
	// citat. Den sidste er den, en note fra Apple Noter er fuld af.
	const markdown = [
		'# Former',
		'',
		'- Punkt',
		'- Punkt 2',
		'',
		'1. Nummer',
		'2. Nummer 2',
		'',
		'10. Ti',
		'11. Elleve',
		'',
		'- Ydre',
		'  - Indre',
		'',
		'> Et citat',
		'> - med en liste i'
	].join('\n');

	const id = await page.evaluate(async (body) => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({ body })
		});
		return (await r.json()).id;
	}, markdown);

	await page.goto('/noter?note=' + id);
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(ed.locator('ul > li').first()).toBeVisible();

	// Vist som lister, ikke som linjer med bindestreger i.
	await expect(ed.locator('ol')).toHaveCount(2);
	await expect(ed.locator('blockquote ul li')).toHaveText('med en liste i');
	// Den indlejrede ligger inde i sit punkt, ikke ved siden af.
	await expect(ed.locator('ul li ul li')).toHaveText('Indre');
	// Og den, der begyndte ved ti, gør det stadig.
	await expect(ed.locator('ol[start="10"] li').first()).toHaveText('Ti');

	// Rør noten, sådan som en tastning gør, og lad den gemme.
	await ed.click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await page.waitForTimeout(1200);

	// Det, der står i filen bagefter, er det, der stod i den før.
	//
	// Det var her, det gik galt: alt fra det første punkt blev til én linje —
	// "PunktPunkt 2NummerNummer 2" — fordi en liste inde i en blok blev læst som
	// tekst. Ingen havde rørt de linjer.
	const body = await page.evaluate(async (noteId) => {
		const r = await fetch('/api/v1/notes/' + noteId, {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		return (await r.json()).body;
	}, id);

	expect(body).toContain('- Punkt\n- Punkt 2');
	expect(body).toContain('1. Nummer\n2. Nummer 2');
	expect(body).toContain('10. Ti\n11. Elleve');
	expect(body).toContain('- Ydre\n  - Indre');
	expect(body).toContain('> Et citat\n> - med en liste i');

	expect(trouble).toEqual([]);
});

test('en note kan ikke smugle et script ind gennem et billedes alt-tekst', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	// Undslap `alt` ikke sine anførselstegn, lukkede den første af dem attributten,
	// og resten stod som markup: et img med en onerror på. En note deles gennem et
	// projekt, så det ville køre på vores eget domæne, hos den der åbnede noten.
	const id = await page.evaluate(async () => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({
				body: '# Ondsindet\n\n![" onerror="window.__pwned = true](/api/v1/attachments/aaaabbbbccccdddd)\n'
			})
		});
		return (await r.json()).id;
	});

	await page.goto('/noter?note=' + id);
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(ed.locator('img')).toHaveCount(1);
	// Billedet peger ingen steder, så det fejler at indlæse — hvilket er præcis
	// den vej, en onerror ville blive kaldt ad.
	await page.waitForTimeout(800);

	expect(await page.evaluate(() => window.__pwned)).toBeUndefined();
	// Og hændelsen står som tekst i alt-attributten, hvor den hører hjemme.
	const alt = await ed.locator('img').getAttribute('alt');
	expect(alt).toContain('onerror');
	expect(await ed.locator('img').evaluate((el) => el.hasAttribute('onerror'))).toBe(false);

	// Billedets egen 404 hører til prøven: adressen peger med vilje på et bilag,
	// der ikke findes, fordi det er den vej en onerror ville blive kaldt ad. Alt
	// andet skal stadig være stille.
	expect(trouble.filter((t) => !t.includes('/api/v1/attachments/'))).toEqual([]);
});

test('et billede kan indsættes og trækkes ind i en note', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	const id = await page.evaluate(async () => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({ body: '# Med billeder\n\nEn linje.\n' })
		});
		return (await r.json()).id;
	});
	await page.goto('/noter?note=' + id);
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(ed).toBeVisible();

	// Et rigtigt lille PNG, bygget i browseren, så prøven ikke har brug for en fil
	// på disken — og lagt i både en paste og et drop, som er de to veje ind.
	const png =
		'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

	for (const kind of ['paste', 'drop']) {
		await ed.click();
		await page.evaluate(
			async ({ kind, png }) => {
				const bytes = Uint8Array.from(atob(png), (c) => c.charCodeAt(0));
				const file = new File([bytes], `${kind}.png`, { type: 'image/png' });
				const dt = new DataTransfer();
				dt.items.add(file);
				const ed = document.querySelector('.page[contenteditable]');
				ed.focus();
				ed.dispatchEvent(
					kind === 'paste'
						? new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })
						: new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true })
				);
			},
			{ kind, png }
		);
		await page.waitForTimeout(1200);
	}

	// To billeder, begge hentet og tegnet — ikke to stykker tekst med ![]( i.
	await expect(ed.locator('img')).toHaveCount(2);
	const loaded = await ed.locator('img').evaluateAll((els) =>
		els.filter((e) => e.complete && e.naturalWidth > 0).length
	);
	expect(loaded).toBe(2);

	// Og de overlever turen til Markdown: filen skal pege på et bilag, ikke på en
	// data-URL, som ville lægge fotografiet i noteteksten.
	const body = await page.evaluate(async (noteId) => {
		const r = await fetch('/api/v1/notes/' + noteId, {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		return (await r.json()).body;
	}, id);
	expect(body.match(/!\[\]\(\/api\/v1\/attachments\/[0-9a-f-]+\)/g)).toHaveLength(2);
	expect(body).not.toContain('data:image');

	expect(trouble).toEqual([]);
});

test('en note kan oprettes inde fra et projekt og hører til det med det samme', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByLabel('Nyt projekt').click();
	await sidebar.getByLabel('Projektnavn').fill('Tagprojekt');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await sidebar.getByRole('link', { name: 'Tagprojekt' }).click();
	await expect(page.getByRole('heading', { name: 'Tagprojekt' })).toBeVisible();

	// Ruden findes, også før der er en eneste note — ellers er der ingen knap at
	// trykke på det sted, hvor man opdager at der skal skrives noget ned.
	await page.getByRole('button', { name: 'Ny note' }).click();

	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(ed).toBeVisible();
	await ed.click();
	await page.keyboard.type('Tagsten og lægter');
	await page.waitForTimeout(1200);

	// Den hører til projektet med det samme: "Læg i projekt" står på projektet,
	// uden at nogen har valgt det bagefter. Kontrollen ligger i Del-popoveren.
	await page.locator('.sharewrap > button').click();
	await expect(page.getByLabel('Læg i projekt')).toHaveValue(/.+/);
	const shared = await page.getByLabel('Læg i projekt').evaluate((el) => el.selectedOptions[0].textContent);
	expect(shared).toContain('Tagprojekt');

	// Og den står på projektets egen side.
	await sidebar.getByRole('link', { name: 'Tagprojekt' }).click();
	// Titlen, ikke uddraget: begge indeholder de samme ord, fordi titlen *er*
	// notens første linje.
	await expect(page.locator('.notes strong', { hasText: 'Tagsten og lægter' })).toBeVisible();

	expect(trouble).toEqual([]);
});

test('en note kan trækkes hen på et projekt i sidebjælken', async ({ page }) => {
	const trouble = watchForTrouble(page);

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await page.goto('/');
	await sidebar.getByLabel('Nyt projekt').click();
	await sidebar.getByLabel('Projektnavn').fill('Trækmål');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(sidebar.getByRole('link', { name: 'Trækmål' })).toBeVisible();

	const id = await page.evaluate(async () => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({ body: '# Løs note\n\nSkal flyttes ved at trække.\n' })
		});
		return (await r.json()).id;
	});

	await page.goto('/noter?note=' + id);
	await page.evaluate((noteId) => (window.__noteId = noteId), id);
	const row = page.locator('.notes button.row').filter({ hasText: 'Løs note' }).first();
	await expect(row).toBeVisible();

	// Selve gesten, med de rigtige hændelser og den nyttelast, appen sender.
	// Rækken tog imod dragover og lod slippet falde ned i grenen, der omarrangerer
	// projekter — så den lyste op og gjorde ingenting. Prøven skal derfor gå hele
	// vejen til slippet og se på resultatet, ikke på at rækken blev fremhævet.
	await page.evaluate(() => {
		const dt = new DataTransfer();
		dt.setData('application/x-verdande-note', window.__noteId);
		const target = [...document.querySelectorAll('nav a')].find((a) =>
			a.textContent.includes('Trækmål')
		);
		for (const type of ['dragover', 'drop']) {
			target.dispatchEvent(new DragEvent(type, { dataTransfer: dt, bubbles: true, cancelable: true }));
		}
	});
	await page.waitForTimeout(1000);

	// Det, der tæller: noten ligger i projektet bagefter.
	const body = await page.evaluate(async (noteId) => {
		const r = await fetch('/api/v1/notes/' + noteId, {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		return await r.json();
	}, id);
	expect(body.project_id).toBeTruthy();

	expect(trouble).toEqual([]);
});

test('en note kan arkiveres fra sin egen fod, og hentes frem igen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();
	await page.keyboard.type('Kvartalsplan');
	await ed.blur();
	await page.waitForTimeout(700);

	// The Arkivér button in the note's footer puts it away: it leaves the list.
	await page.locator('footer .actions .button', { hasText: 'Arkivér' }).click();
	await expect(page.locator('.notes button.row', { hasText: 'Kvartalsplan' })).toHaveCount(0);

	// It is in the archive, where the same button now offers to bring it back.
	await page.getByRole('button', { name: 'Vis arkivet' }).click();
	await page.locator('.notes button.row', { hasText: 'Kvartalsplan' }).click();
	await page.locator('footer .actions .button', { hasText: 'Tag frem igen' }).click();
	await expect(page.locator('.notes button.row', { hasText: 'Kvartalsplan' })).toHaveCount(0);

	// And back in the ordinary list.
	await page.getByRole('button', { name: 'Vis arkivet' }).click();
	await expect(page.locator('.notes button.row', { hasText: 'Kvartalsplan' })).toBeVisible();

	expect(trouble).toEqual([]);
});

test('en monospace-linje kan forlades — på retur og gennem menuen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	await page.getByRole('button', { name: 'Ny note' }).click();
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	await ed.click();
	await page.keyboard.type('Titel');
	await page.keyboard.press('Enter');

	// En monospace-linje, og så retur: linjen efter skal være brødtekst, ikke mere
	// monospace. Før rettelsen holdt en <pre> på markøren, og alt derefter blev ved
	// med at være kode.
	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Monotype' }).click();
	await page.keyboard.type('kode linje');
	await page.keyboard.press('Enter');
	await page.keyboard.type('almindelig tekst');

	// Koden står i sin blok; teksten efter står uden for den.
	await expect(ed.locator('pre')).toHaveText('kode linje');
	await expect(ed.locator('p').filter({ hasText: 'almindelig tekst' })).toBeVisible();
	await expect(ed.locator('pre').filter({ hasText: 'almindelig tekst' })).toHaveCount(0);

	// Shift+retur bliver derimod i blokken, så flerlinjet kode stadig kan skrives.
	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Monotype' }).click();
	await page.keyboard.type('linje et');
	await page.keyboard.press('Shift+Enter');
	await page.keyboard.type('linje to');
	await expect(ed.locator('pre').filter({ hasText: 'linje et' })).toContainText('linje to');

	// Og menuen kan føre en monospace-linje tilbage til brødtekst — dét, der før
	// lagde et <p> inde i <pre> og ikke kom nogen vegne.
	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Brødtekst' }).click();
	// Den blok er nu brødtekst, ikke kode: den står som <p>, ikke i en <pre>.
	await expect(ed.locator('p').filter({ hasText: 'linje et' })).toBeVisible();
	await expect(ed.locator('pre').filter({ hasText: 'linje et' })).toHaveCount(0);

	// Det overlever turen gennem Markdown. En ren note for sig, så prøven ikke
	// afhænger af alt det ovenfor: kode, retur, brødtekst — gem, hent igen.
	await page.getByRole('button', { name: 'Ny note' }).click();
	await ed.click();
	await page.keyboard.type('Rundtur');
	await page.keyboard.press('Enter');
	await page.getByRole('button', { name: 'Formatér' }).click();
	await page.getByRole('menuitem', { name: 'Monotype' }).click();
	await page.keyboard.type('docker pull');
	await page.keyboard.press('Enter');
	await page.keyboard.type('og så videre i almindelig skrift');
	await ed.blur();
	await page.waitForTimeout(1200);
	await page.reload();
	await page.getByRole('button', { name: /Rundtur/ }).click();
	await expect(ed.locator('pre')).toContainText('docker pull');
	await expect(ed.locator('p').filter({ hasText: 'almindelig skrift' })).toBeVisible();
	await expect(ed.locator('pre').filter({ hasText: 'almindelig skrift' })).toHaveCount(0);

	expect(trouble).toEqual([]);
});

test('kodeblokken har en kopiér-knap, og knappen havner ikke i filen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/noter');

	const id = await page.evaluate(async () => {
		const r = await fetch('/api/v1/notes', {
			method: 'POST',
			credentials: 'include',
			headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
			body: JSON.stringify({ body: '# Kommandoen\n\n```bash\ndocker pull verdande\n```\n' })
		});
		return (await r.json()).id;
	});
	await page.goto('/noter?note=' + id);

	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	const copy = ed.locator('pre .copy');
	await expect(copy).toBeVisible();
	await copy.click();
	await expect(copy).toHaveText('Kopieret');

	// Det, der ligger i udklipsholderen, er koden — ikke knappens eget ord.
	const clip = await page.evaluate(() => navigator.clipboard.readText());
	expect(clip).toBe('docker pull verdande');

	// Og noten selv må ikke få "Kopiér" skrevet ind i sig, når den gemmes. Knappen
	// ligger inde i <pre> for at kunne stå i hjørnet af den, så det er præcis den
	// fælde, den her prøve findes for.
	await ed.click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await page.waitForTimeout(1200);

	const body = await page.evaluate(async (noteId) => {
		const r = await fetch('/api/v1/notes/' + noteId, {
			credentials: 'include',
			headers: { 'Sec-Fetch-Site': 'same-origin' }
		});
		return (await r.json()).body;
	}, id);
	expect(body).toContain('docker pull verdande');
	expect(body).not.toContain('Kopiér');
	expect(body).not.toContain('Kopieret');

	expect(trouble).toEqual([]);
});

test('flere noter kan markeres, arkiveres og hentes frem igen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	for (const title of ['Alfa', 'Bravo', 'Charlie']) {
		await page.evaluate(async (t) => {
			await fetch('/api/v1/notes', {
				method: 'POST',
				credentials: 'include',
				headers: { 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin' },
				body: JSON.stringify({ body: `# ${t}\n\nEn note.\n` })
			});
		}, title);
	}
	await page.reload();
	const rows = page.locator('.notes button.row');
	await expect(rows.filter({ hasText: 'Alfa' })).toBeVisible();

	// To af tre: et almindeligt klik, og et ⌘-klik oven i. Den, der lige er åbnet,
	// hører med i markeringen — ellers ville det andet klik markere én og glemme
	// den, man stod på.
	await rows.filter({ hasText: 'Alfa' }).click();
	await rows.filter({ hasText: 'Bravo' }).click({ modifiers: ['Meta'] });
	await expect(page.getByText('2 markeret')).toBeVisible();

	// The selection bar's Arkivér, not the open note's own in the footer.
	await page.locator('.picked-bar').getByRole('button', { name: 'Arkivér', exact: true }).click();
	await page.waitForTimeout(900);

	// Væk fra listen, og ikke i papirkurven: arkivering er ikke sletning.
	await expect(rows.filter({ hasText: 'Alfa' })).toHaveCount(0);
	await expect(rows.filter({ hasText: 'Charlie' })).toBeVisible();

	await page.getByLabel('Vis arkivet').click();
	await page.waitForTimeout(900);
	await expect(rows.filter({ hasText: 'Alfa' })).toBeVisible();
	await expect(rows.filter({ hasText: 'Bravo' })).toBeVisible();
	await expect(rows.filter({ hasText: 'Charlie' })).toHaveCount(0);

	// Og tilbage igen — med rækkens egen knap, som er vejen for den ene. Et
	// almindeligt klik rydder markeringen med vilje, så bjælken er til flere.
	await rows.filter({ hasText: 'Alfa' }).hover();
	await page.locator('.rowline', { hasText: 'Alfa' }).getByLabel('Tag frem igen').click();
	await page.waitForTimeout(900);
	await expect(rows.filter({ hasText: 'Alfa' })).toHaveCount(0);

	await page.getByLabel('Vis arkivet').click();
	await page.waitForTimeout(900);
	await expect(rows.filter({ hasText: 'Alfa' })).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * Én note, der ikke kan tegnes, må ikke tage ruden med sig.
 *
 * Meldt ind som "jeg kan ikke skifte mellem noter — uanset hvilken jeg trykker
 * på, åbner de ikke". Editoren indlæste med `note.body.trim()` uden værn, og
 * `body` er en streng fra serveren hver eneste gang — indtil den ikke er. Så
 * kaster den inde i et `$effect`, og en effekt, der kaster, tager komponentens
 * opdateringer med sig: den forrige note bliver stående, og ingen note kan åbnes
 * bagefter. Klikket kan ikke engang lande.
 *
 * Formen fremtvinges her frem for at vente på den nyttelast, der gjorde det:
 * hvad der end fik `body` til at mangle, er svaret det samme — én note må ikke
 * kunne låse de tolv hundrede andre ude.
 */
test('en note uden krop må ikke låse ruden', async ({ page }) => {
	const errors = [];
	page.on('pageerror', (e) => errors.push(e.message));

	await page.goto('/noter');
	for (const title of ['Alfa', 'Beta']) {
		await page.getByRole('button', { name: 'Ny note' }).click();
		const ed = page.getByRole('textbox', { name: 'Notens tekst' });
		await ed.click();
		await page.keyboard.type(title);
		await expect(ed.locator('h1')).toHaveText(title);
		await page.keyboard.press('Enter');
		await page.keyboard.type('Brødtekst i ' + title);
		await expect(ed.locator('p').filter({ hasText: 'Brødtekst i ' + title })).toBeVisible();
		await page.waitForTimeout(900);
	}
	await page.reload();
	await expect(page.locator('li .row strong').filter({ hasText: 'Beta' })).toBeVisible();

	await page.route('**/api/v1/notes/*', async (route) => {
		if (route.request().method() !== 'GET') return route.continue();
		const res = await route.fetch();
		const note = await res.json();
		delete note.body;
		await route.fulfill({ response: res, json: note });
	});

	await page.locator('li').filter({ hasText: 'Alfa' }).first().click();
	await page.waitForTimeout(600);
	expect(errors, 'noten kastede under indlæsningen').toEqual([]);

	await page.unroute('**/api/v1/notes/*');
	await page.locator('li').filter({ hasText: 'Beta' }).first().click();
	await expect(
		page.getByRole('textbox', { name: 'Notens tekst' }).locator('h1'),
		'ruden er låst: den næste note åbner ikke'
	).toHaveText('Beta', { timeout: 5000 });
	expect(errors, 'ruden efterlod en fejl bag sig').toEqual([]);
});

/**
 * Ugen er et døgn, ikke syv punktopstillinger.
 *
 * En uge, hvor hver dag er en liste, svarer på "hvad skal der ske torsdag" og ikke
 * på "hvornår" — og det andet er det, man åbner en uge for. To møder klokken ti er
 * en konflikt, man skal kunne se, ikke læse sig til.
 *
 * Begivenhederne kommer fra en rute, der svarer i stedet for Google: det, der
 * prøves her, er gitteret, og en prøve, der først skal have en OAuth-forbindelse,
 * er en prøve, der aldrig kører.
 */
test('ugen viser et døgn, og dagene er lige brede', async ({ page }) => {
	const trouble = watchForTrouble(page);

	// Mandag i den uge, siden åbner på.
	const monday = (n) => {
		const d = new Date();
		d.setDate(d.getDate() - ((d.getDay() + 6) % 7) + n);
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
			d.getDate()
		).padStart(2, '0')}`;
	};
	const at = (n, from, to, id, summary) => ({
		id,
		calendar_id: 'c1',
		summary,
		starts_at: `${monday(n)}T${from}:00+02:00`,
		ends_at: `${monday(n)}T${to}:00+02:00`,
		start_day: monday(n),
		end_day: monday(n),
		all_day: false,
		calendar_name: 'Privat'
	});

	await page.route('**/api/v1/calendar/events*', (route) =>
		route.fulfill({
			json: {
				events: [
					at(2, '09:00', '10:00', 'e1', 'Morgenmøde'),
					at(2, '09:30', '11:00', 'e2', 'Oveni det første'),
					at(2, '17:00', '18:00', 'e3', 'Sent på dagen'),
					{
						id: 'e4',
						calendar_id: 'c1',
						summary: 'Aalborg',
						start_day: monday(1),
						end_day: monday(1),
						all_day: true,
						calendar_name: 'Privat'
					}
				]
			}
		})
	);

	await page.goto('/kalender');
	await page.getByRole('button', { name: 'Uge', exact: true }).click();
	await expect(page.locator('.weekgrid')).toBeVisible();

	// Syv lige brede dage. `1fr` er `minmax(auto, 1fr)`, så ét langt ord i én dag
	// gjorde den bredere end de seks andre — hvilket er det modsatte af et gitter.
	const widths = await page
		.locator('.weekgrid .daycol')
		.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().width)));
	expect(widths, 'ugen har ikke syv søjler').toHaveLength(7);
	expect(Math.max(...widths) - Math.min(...widths), 'dagene er ikke lige brede').toBeLessThanOrEqual(1);

	// Klokken bestemmer, hvor noget står. Morgenmødet skal ligge over det sene.
	const box = async (name) => (await page.locator('.tevent', { hasText: name }).first().boundingBox());
	const morning = await box('Morgenmøde');
	const evening = await box('Sent på dagen');
	expect(morning.y, 'morgenmødet står ikke over det sene møde').toBeLessThan(evening.y);

	// To ting på samme tid står ved siden af hinanden. Oven på hinanden er præcis
	// den konflikt, visningen findes for at vise.
	const overlapping = await box('Oveni det første');
	expect(overlapping.x, 'de to overlappende ligger oven på hinanden').toBeGreaterThan(morning.x);
	expect(overlapping.x, 'den anden er havnet i næste dag').toBeLessThan(morning.x + morning.width * 2);

	// Heldagsting hører til båndet foroven: en heldagsbegivenhed lagt klokken nul
	// ville påstå, den holdt fra midnat til midnat.
	// `.weekgrid > .allday` er selve cellen; brikken indeni bærer også klassen.
	await expect(page.locator('.weekgrid > .allday', { hasText: 'Aalborg' })).toBeVisible();

	// Og måneden er stadig lige bred hele vejen rundt.
	await page.getByRole('button', { name: 'Måned', exact: true }).click();
	const monthWidths = await page
		.locator('.grid .day')
		.evaluateAll((els) => els.slice(0, 7).map((e) => Math.round(e.getBoundingClientRect().width)));
	expect(
		Math.max(...monthWidths) - Math.min(...monthWidths),
		'månedens dage er ikke lige brede'
	).toBeLessThanOrEqual(1);

	expect(trouble).toEqual([]);
});

/**
 * Tid på en opgave, sat i ruden og justeret i kalenderen.
 *
 * Tre påstande, og den midterste er den, der var gået galt uden at nogen sagde
 * det: serveren læser `due_date` og `due_time` sammen, så en dato sendt uden en
 * tid *rydder* tiden. Et gem af datoen alene tog altså klokkeslættet med sig, og
 * det samme gjorde et træk i kalenderen.
 */
test('en opgave kan få et klokkeslæt, og beholder det når den flyttes', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const box = page.getByLabel('Ny opgave');
	// I dag, så den står på den visning, den skrives i — og i den uge, gitteret
	// åbner på længere nede.
	await box.fill('ringe til tandlægen i dag');
	await box.press('Enter');
	await expect(page.getByText('ringe til tandlægen')).toBeVisible();

	// Klokkeslættet sættes i ruden. Feltet er tomt til at begynde med: en opgave
	// uden tid er det almindelige, og 00:00 ville påstå noget om midnat.
	await page.getByText('ringe til tandlægen').click();
	const time = page.getByLabel('Klokkeslæt');
	await expect(time).toHaveValue('');
	// Tidligt på dagen med vilje: gitteret begynder klokken otte, så opgaven står
	// lige under båndet og de to kasser er i billedet på én gang. Klokken fjorten
	// ligger under skærmkanten, og et træk, der først skal rulle, er et træk,
	// browseren taber.
	await time.fill('08:30');
	await time.blur();
	await page.waitForTimeout(400);

	// Gemt hos serveren, ikke kun i feltet: genindlæst og åbnet igen.
	await page.reload();
	await page.getByText('ringe til tandlægen').click();
	await expect(page.getByLabel('Klokkeslæt'), 'klokkeslættet blev ikke gemt').toHaveValue('08:30');

	// Og datoen kan rettes bagefter uden at tage timen med sig.
	const date = page.locator('#due');
	const day = await date.inputValue();
	await date.fill(day);
	await date.blur();
	await page.waitForTimeout(400);
	await page.reload();
	await page.getByText('ringe til tandlægen').click();
	await expect(
		page.getByLabel('Klokkeslæt'),
		'et gem af datoen ryddede klokkeslættet'
	).toHaveValue('08:30');

	await page.keyboard.press('Escape');

	// I ugegitteret står den på sit klokkeslæt frem for i heldagsbåndet.
	await page.goto('/upcoming');
	await page.getByRole('button', { name: 'Uge', exact: true }).click();
	const placed = page.locator('.tevent.task').filter({ hasText: 'ringe til tandlægen' });
	await expect(placed, 'opgaven står ikke på døgnet').toBeVisible();
	await expect(placed).toContainText('08:30');

	// Trukket op i båndet over sin egen dag: så har den en dag og ingen tid. Den
	// tomme streng siger "ryd", hvor ingenting siger "behold" — og de to må ikke
	// forveksles.
	//
	// Et bånd, der er tomt. Prøven handler om at krydse mellem de to kasser, og en
	// dag, hvis bånd allerede er fyldt af andre prøvers opgaver, gør slippunktet
	// til en anden opgave frem for til båndet — hvilket er en anden gest end den,
	// der prøves her.
	const band = page
		.locator('.weekgrid > .allday')
		.filter({ hasNot: page.locator('.chip, .event') })
		.first();
	const today = await band.getAttribute('data-allday');
	// Trukket i skridt frem for i ét spring. `dragTo` flytter musen i to træk, og
	// over fire hundrede pixels i et travlt gitter giver det browseren for få
	// mellemstationer til at holde gesten i live. Et menneske bevæger hånden hele
	// vejen, og det er også det, der skal prøves.
	const fromBox = await placed.boundingBox();
	const toBox = await band.boundingBox();
	await page.mouse.move(fromBox.x + fromBox.width / 2, fromBox.y + 5);
	await page.mouse.down();
	await page.mouse.move(toBox.x + toBox.width / 2, toBox.y + toBox.height / 2, { steps: 25 });
	await page.mouse.up();
	await expect(
		page.locator(`[data-allday="${today}"] .chip`).filter({ hasText: 'ringe til tandlægen' }),
		'opgaven kom ikke op i båndet'
	).toBeVisible();
	await expect(
		page.locator(`[data-date="${today}"] .tevent`).filter({ hasText: 'ringe til tandlægen' }),
		'opgaven står stadig på et klokkeslæt'
	).toBeHidden();

	expect(trouble).toEqual([]);
});

/**
 * Notelisten står i grupper.
 *
 * Den føltes tæt, og grunden var ikke afstanden mellem rækkerne: hver række sagde
 * tre ting, og to af dem gentog sig selv nedad. Datoen stod på hver eneste række,
 * også når tyve noter deler dag, og projektet fik en linje for sig selv.
 *
 * Datoen er flyttet op i en overskrift, favoritterne har fået deres egen gruppe —
 * de lå allerede først, der stod bare ikke hvorfor — og arkivet grupperes på samme
 * måde, for det er dér, de tolv hundrede importerede ligger.
 */
test('notelisten grupperer favoritter og datoer, også i arkivet', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/noter');

	for (const title of ['Gruppe én', 'Gruppe to']) {
		await page.getByRole('button', { name: 'Ny note' }).click();
		const ed = page.getByRole('textbox', { name: 'Notens tekst' });
		await ed.click();
		await page.keyboard.type(title);
		await page.keyboard.press('Enter');
		await page.keyboard.type('Brødtekst i ' + title);
		await expect(ed.locator('h1')).toHaveText(title);
		await page.waitForTimeout(800);
	}
	await page.reload();

	// Skrevet i dag, så de står under dagens overskrift.
	const today = page.getByRole('heading', { name: /^I dag/ });
	await expect(today, 'der er ingen datogruppe').toBeVisible();

	// En favorit får sin egen gruppe. Uden overskriften ligger den bare først, og
	// der står ikke hvorfor.
	await expect(page.getByRole('heading', { name: /Favoritter/i })).toBeHidden();
	const row = page.locator('li').filter({ hasText: 'Gruppe én' }).first();
	await row.hover();
	await row.getByRole('button', { name: /favorit/i }).click();
	const favourites = page.getByRole('heading', { name: /Favoritter/i });
	await expect(favourites).toBeVisible();

	// Og den ligger under den, ikke under dagen.
	const inFavourites = page
		.locator('h3.group', { hasText: /Favoritter/i })
		.locator('+ ul li')
		.filter({ hasText: 'Gruppe én' });
	await expect(inFavourites).toBeVisible();

	// Grupper kan foldes sammen. Ikke husket på tværs af besøg: en gruppe, der
	// åbner foldet uden at nogen bad om det, er en liste, der ser tom ud.
	await favourites.getByRole('button').click();
	await expect(inFavourites).toBeHidden();
	await favourites.getByRole('button').click();
	await expect(inFavourites).toBeVisible();

	// En søgning grupperes ikke: serveren har sorteret efter hvor godt hver note
	// matchede, og at dele det op i måneder ville stille rækkefølgen om efter noget
	// andet end det, der blev spurgt om.
	await page.getByPlaceholder('Søg i noter').fill('Brødtekst');
	await expect(page.locator('li').filter({ hasText: 'Gruppe to' }).first()).toBeVisible();
	await expect(page.locator('h3.group')).toHaveCount(0);
	await page.getByPlaceholder('Søg i noter').fill('');

	// Rækkerne skal kunne *ses*, ikke bare findes.
	//
	// Da listen blev delt i grupper, blev hver gruppe sit eget `<ul>` — og dermed
	// sit eget flex-element med `overflow-y: auto`. Flex-elementer skrumper som
	// udgangspunkt, og et element, der skrumper og klipper, viser ingenting: alle
	// overskrifter stod med deres tal, og der var ikke en eneste note under nogen af
	// dem. Med tre grupper og en høj skærm var der plads nok til at det ikke skete,
	// hvilket er hvorfor prøven herover ikke fangede det.
	//
	// Et lavt vindue fremtvinger presset. Tolv hundrede noter i tyve måneder gør det
	// samme i virkeligheden.
	await page.setViewportSize({ width: 1100, height: 380 });
	await page.waitForTimeout(300);
	await expect(
		page.locator('li').filter({ hasText: 'Gruppe to' }).first(),
		'rækkerne blev klippet væk, da der ikke var plads'
	).toBeVisible();
	// Og rulningen ligger på kassen om grupperne, ikke på hver liste for sig.
	const scroller = await page
		.locator('.list')
		.evaluate((el) => getComputedStyle(el).overflowY);
	expect(scroller, 'kassen om grupperne ruller ikke').toBe('auto');
	const inner = await page
		.locator('.list ul')
		.first()
		.evaluate((el) => getComputedStyle(el).overflowY);
	expect(inner, 'den enkelte liste ruller stadig for sig').toBe('visible');
	await page.setViewportSize({ width: 1280, height: 720 });

	// Arkivet grupperes på samme måde. Det er dér, det betyder mest.
	await row.hover();
	await row.getByRole('button', { name: /Arkivér/i }).click();
	await page.getByRole('button', { name: 'Vis arkivet' }).click();
	await expect(
		page.locator('h3.group'),
		'arkivet står ugrupperet'
	).not.toHaveCount(0);
	await expect(page.locator('li').filter({ hasText: 'Gruppe én' }).first()).toBeVisible();

	expect(trouble).toEqual([]);
});

/**
 * Et projekts kalender kan vises som en uge.
 *
 * Ugen fandtes på Kommende og på Kalender, men ikke dér, hvor et projekt bor — og
 * det er dér, "hvornår sker de her fire ting" oftest bliver spurgt.
 *
 * Inde i kalendervisningen frem for som en fjerde `view_mode`. Den er projektets
 * og delt med alle, der kan se det, mens uge eller måned er et spørgsmål om, hvor
 * bred skærmen er, foran hvilken der sidder én person — og en fjerde værdi ville
 * kræve en tabelombygning, fordi SQLite ikke kan ændre et CHECK på plads.
 */
test('et projekts kalender kan vises som en uge', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	const sidebar = page.getByRole('navigation', { name: 'Hovedmenu' });
	await sidebar.getByRole('button', { name: 'Nyt projekt' }).click();
	await sidebar.getByLabel('Projektnavn').fill('Ugeprojekt');
	await sidebar.getByLabel('Projektnavn').press('Enter');
	await expect(page.getByRole('heading', { name: 'Ugeprojekt' })).toBeVisible();

	const box = page.getByLabel('Ny opgave');
	await box.fill('male døren i dag kl 10');
	await box.press('Enter');
	await expect(page.getByText('male døren')).toBeVisible();

	await page.getByRole('button', { name: 'Kalender', exact: true }).click();
	await page.getByRole('button', { name: 'Uge', exact: true }).click();

	// Døgnet, ikke en rude: opgaven står på sit klokkeslæt.
	const placed = page.locator('.tevent.task').filter({ hasText: 'male døren' });
	await expect(placed, 'opgaven står ikke på døgnet').toBeVisible();
	await expect(placed).toContainText('10:00');

	// Valget huskes — i browseren, ikke på projektet: det er skærmens bredde, der
	// afgør det, og projektet er delt med andre skærme.
	await page.reload();
	await expect(page.locator('.weekgrid'), 'ugen blev ikke husket').toBeVisible();

	await page.getByRole('button', { name: 'Måned', exact: true }).click();
	await expect(page.locator('.weekgrid')).toBeHidden();

	expect(trouble).toEqual([]);
});

/**
 * En gentagelse siges på læserens sprog, også dem serveren kun kan på dansk.
 *
 * `recurrence.Describe` skriver dansk og kan ikke bare tage en locale med:
 * `toTaskJSON` har niogtyve kaldesteder, og mange er WebSocket-udsendelser, hvor én
 * nyttelast når alle i et projekt på én gang — der findes ikke ét sprog at skrive
 * den i. Reglen er den samme uanset hvem der læser den, så beskrivelsen hører
 * hjemme dér, hvor læseren er.
 *
 * De to her er valgt, fordi de *ikke* står på listen over de fem almindelige, som
 * fladen altid har kunnet navngive. Før det her faldt de tilbage til serverens
 * tekst — og den er dansk, hvad end kontoen er sat til.
 */
test('en gentagelse, serveren kun kan på dansk, siges alligevel i fladen', async ({ page }) => {
	const trouble = watchForTrouble(page);
	await page.goto('/');

	for (const [text, says] of [
		['vande planterne hver mandag', 'hver mandag'],
		['tømme opvasker hverdage', 'hverdage']
	]) {
		const box = page.getByLabel('Ny opgave');
		await box.fill(text);
		await box.press('Enter');
		await page.waitForTimeout(400);

		// I indbakken og ikke på I dag: en ugentlig regel sætter den første
		// forfaldsdag til den næste sådan dag, som sjældent er i dag.
		await page
			.getByRole('navigation', { name: 'Hovedmenu' })
			.getByRole('link', { name: /^Indbakke/ })
			.click();

		const name = text.split(' ').slice(0, 2).join(' ');
		const row = page.locator('.row', { hasText: name }).first();
		await expect(row).toBeVisible();
		await expect(
			row.locator('.repeat'),
			`"${text}" blev ikke læst som en gentagelse`
		).toContainText(says);

		await page.goto('/');
	}

	expect(trouble).toEqual([]);
});
