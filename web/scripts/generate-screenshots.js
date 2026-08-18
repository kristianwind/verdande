/**
 * Captures the screenshots the landing page, the README and the docs use.
 *
 *     node scripts/generate-screenshots.js
 *
 * A script rather than a test, for the same reason as generate-icons.js: CI must
 * never rewrite a tracked binary file. Run it when the interface changes.
 *
 * It replaces the hand-driven half of `go run ./tools/shots`, which needs a
 * running instance and a session cookie pasted in by hand — and which therefore
 * ran rarely enough that the images fell many rounds behind the app they show.
 * This one starts the real binary, creates its own account, fills it with the
 * demo data, and takes the pictures. Nothing to set up and nothing to remember.
 *
 * The binary, not the dev server: the screenshots should show what ships. That
 * means building the frontend and copying it into cmd/verdande/webbuild first,
 * exactly as the smoke tests do — a capture of a stale embedded build would be a
 * picture of an app nobody is running.
 */
import { chromium } from '@playwright/test';
import { spawn, execSync } from 'node:child_process';
import { mkdirSync, rmSync, writeFileSync, copyFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const repo = join(web, '..');
const dataDir = join(web, '.screenshot-data');

const PORT = 8098;
const baseURL = `http://127.0.0.1:${PORT}`;
const USER = { name: 'Kristian', email: 'kristian@example.dk', password: 'et langt kodeord' };

// 1400×900 is what the landing page's <img> declares. A capture at another size
// would be resampled by the browser and look soft next to the text beside it.
const WIDTH = 1400;
const HEIGHT = 900;

const OUT = [join(repo, 'site', 'screenshots'), join(repo, 'docs', 'screenshots')];

/**
 * The pictures, and why each one is here.
 *
 * Every view that carries a claim the prose makes. A screenshot set that stops at
 * "the task list" is a set that cannot show anything the app grew since.
 */
const SHOTS = [
	{ name: 'today', path: '/', theme: 'dark' },
	{ name: 'today-light', path: '/', theme: 'light' },
	{ name: 'upcoming', path: '/upcoming', theme: 'dark', prepare: showMonth },
	{ name: 'week', path: '/upcoming', theme: 'dark', prepare: showWeek },
	{ name: 'board', path: (ids) => `/projekt/${ids.work}`, theme: 'dark' },
	// The same project as a list, which is where sections read as bands rather than
	// as columns — and the shape most people actually work in.
	{ name: 'sections', path: (ids) => `/projekt/${ids.work}`, theme: 'dark', prepare: showList },
	{ name: 'detail', path: '/', theme: 'dark', prepare: openFirstTask },
	{ name: 'settings', path: '/indstillinger/integrationer', theme: 'dark' },
	{ name: 'themes', path: '/indstillinger', theme: 'dark' },
	{ name: 'admin', path: '/indstillinger/brugere', theme: 'dark' },
	{ name: 'history', path: '/indstillinger/historik', theme: 'dark' },
	{ name: 'delegated', path: '/uddelegeret', theme: 'dark' }
];

async function showList(page) {
	await page.getByRole('button', { name: 'Liste', exact: true }).click();
	await page.waitForTimeout(600);
}

async function showMonth(page) {
	await page.getByRole('button', { name: 'Måned', exact: true }).click();
	await page.waitForTimeout(800);
}

async function showWeek(page) {
	await page.getByRole('button', { name: 'Uge', exact: true }).click();
	await page.waitForTimeout(800);
}

async function openFirstTask(page) {
	await page.locator('.row').first().click();
	await page.waitForTimeout(600);
}

console.log('building the frontend and embedding it…');
execSync('npm run build', { cwd: web, stdio: 'inherit' });
rmSync(join(repo, 'cmd', 'verdande', 'webbuild'), { recursive: true, force: true });
execSync(`cp -r ${JSON.stringify(join(web, 'build'))} ${JSON.stringify(join(repo, 'cmd', 'verdande', 'webbuild'))}`);

// A clean instance: the first screen is "create the first account", which only
// appears on an empty database.
rmSync(dataDir, { recursive: true, force: true });

console.log('starting the binary…');
// `detached`, so the whole process group can be killed at the end. `go run`
// compiles the binary and runs it as a *child*: killing `go run` leaves the server
// itself holding the port, and the next run then dies on "address already in use"
// with no clue as to what is holding it.
const server = spawn('go', ['run', '-tags', 'embedweb', './cmd/verdande'], {
	cwd: repo,
	detached: true,
	stdio: ['ignore', 'inherit', 'inherit'],
	env: {
		...process.env,
		VERDANDE_ADDR: `:${PORT}`,
		VERDANDE_BASE_URL: baseURL,
		VERDANDE_DATA_DIR: dataDir,
		// An update check here would reach out to GitHub to take a screenshot.
		VERDANDE_UPDATE_CHECK: 'false'
	}
});

let browser;
try {
	await waitForHealth();

	browser = await chromium.launch();
	const page = await browser.newPage({
		viewport: { width: WIDTH, height: HEIGHT },
		// The same pinning the smoke tests use, and for the same reason: the sign-up
		// form sends navigator.language as the account's locale, and every due date
		// is resolved in the account's zone.
		locale: 'da-DK',
		timezoneId: 'Europe/Copenhagen',
		deviceScaleFactor: 2
	});

	await page.goto(baseURL);
	await page.getByLabel(/Navn/).fill(USER.name);
	await page.getByLabel(/E-mail/).fill(USER.email);
	await page.getByLabel(/Adgangskode/).fill(USER.password);
	await page.getByRole('button', { name: 'Opret konto' }).click();
	await page.getByRole('navigation', { name: 'Hovedmenu' }).waitFor();

	console.log('seeding…');
	const ids = await page.evaluate(seed);

	mkdirSync(OUT[0], { recursive: true });
	mkdirSync(OUT[1], { recursive: true });

	for (const shot of SHOTS) {
		const path = typeof shot.path === 'function' ? shot.path(ids) : shot.path;
		await page.goto(baseURL + path);
		// A fixed settle rather than a load event: the interesting part is after
		// hydration and after the first fetch, and the load event says nothing
		// about either.
		await page.waitForTimeout(1200);
		if (shot.prepare) await shot.prepare(page);
		if (shot.theme) {
			await page.evaluate((t) => (document.documentElement.dataset.theme = t), shot.theme);
			await page.waitForTimeout(400);
		}
		const png = await page.screenshot();
		writeFileSync(join(OUT[0], `${shot.name}.png`), png);
		copyFileSync(join(OUT[0], `${shot.name}.png`), join(OUT[1], `${shot.name}.png`));
		console.log(`  ${shot.name}.png`);
	}
} finally {
	await browser?.close();
	// The group, not the process: see the spawn above.
	try {
		process.kill(-server.pid, 'SIGKILL');
	} catch {
		// Already gone, which is the outcome this was for.
	}
	rmSync(dataDir, { recursive: true, force: true });
}

async function waitForHealth() {
	for (let i = 0; i < 240; i++) {
		try {
			const r = await fetch(`${baseURL}/healthz`);
			if (r.ok) return;
		} catch {
			// Not up yet. The first run compiles the binary, which takes a while.
		}
		await new Promise((r) => setTimeout(r, 500));
	}
	throw new Error('the server never came up');
}

/**
 * The demo data, created through the app's own API from inside the page.
 *
 * From the page rather than from node, so the session cookie and the same-origin
 * header the API insists on come along without being reconstructed here. Written
 * as one function because it is passed to the browser as source.
 *
 * The content is ordinary Danish household and work tasks. Screenshots of "Task
 * 1, Task 2, Task 3" show the layout and hide whether the app is any good at the
 * thing it is for.
 */
async function seed() {
	const api = async (path, body, method = 'POST') => {
		const r = await fetch(`/api/v1${path}`, {
			method,
			headers: { 'Content-Type': 'application/json' },
			body: body ? JSON.stringify(body) : undefined
		});
		if (!r.ok) throw new Error(`${method} ${path} → ${r.status}`);
		return r.status === 204 ? null : r.json();
	};

	const day = (offset) => {
		const d = new Date();
		d.setDate(d.getDate() + offset);
		return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
			d.getDate()
		).padStart(2, '0')}`;
	};

	// Two headings over the projects, which is what the sidebar grew for.
	const arbejde = await api('/project-groups', { name: 'Arbejde' });
	const privat = await api('/project-groups', { name: 'Privat' });
	await api(`/project-groups/${arbejde.id}`, { color: 'indigo' }, 'PATCH');
	await api(`/project-groups/${privat.id}`, { color: 'green' }, 'PATCH');

	const work = await api('/projects', {
		name: 'Sæsonstart',
		color: 'indigo',
		view_mode: 'board',
		group_id: arbejde.id
	});
	const house = await api('/projects', { name: 'Huset', color: 'amber', group_id: privat.id });
	const garden = await api('/projects', { name: 'Haven', color: 'green', group_id: privat.id });

	const planning = await api(`/projects/${work.id}/sections`, { name: 'Planlægning' });
	const inFlight = await api(`/projects/${work.id}/sections`, { name: 'I gang' });
	const done = await api(`/projects/${work.id}/sections`, { name: 'Til gennemsyn' });

	const tasks = [
		// Today, in the Inbox and across the projects: the Today view is the first
		// picture and it has to look like a real day.
		{ content: 'Ring til tømreren om taget', due_date: day(0), priority: 1, project_id: house.id },
		{ content: 'Aflever cyklen til service', due_date: day(0), priority: 3, labels: ['ærinder'] },
		{
			content: 'Skriv oplæg til sæsonmødet',
			due_date: day(0),
			priority: 2,
			project_id: work.id,
			section_id: planning.id,
			duration_min: 90
		},
		{ content: 'Vand drivhuset', due_date: day(0), recurrence_rule: 'FREQ=DAILY', project_id: garden.id },
		{ content: 'Hent pakken på posthuset', due_date: day(0), labels: ['ærinder'] },

		// The next couple of weeks, so Kommende and the month grid have something in
		// them rather than a row of empty cells.
		{ content: 'Book håndværker til badeværelset', due_date: day(1), priority: 2, project_id: house.id },
		{ content: 'Beskær den grønne hæk', due_date: day(2), project_id: garden.id },
		{ content: 'Gennemgå budgettet', due_date: day(3), priority: 1, project_id: work.id, section_id: inFlight.id },
		{ content: 'Bestil løg til efteråret', due_date: day(5), project_id: garden.id },
		{ content: 'Skift dæk', due_date: day(8), priority: 3 },
		{ content: 'Send referat ud', due_date: day(9), project_id: work.id, section_id: done.id },
		{ content: 'Male vindueskarmene', due_date: day(12), project_id: house.id, priority: 2 },
		{ content: 'Så vinterurter', due_date: day(15), project_id: garden.id },
		{ content: 'Kvartalsopfølgning', due_date: day(21), priority: 1, project_id: work.id, section_id: planning.id },
		{ content: 'Rens tagrenderne', due_date: day(26), project_id: house.id },

		// A few without dates, so the board has cards that are not all deadlines.
		{ content: 'Find ny leverandør af emballage', project_id: work.id, section_id: planning.id, priority: 3 },
		{ content: 'Skriv stillingsopslag', project_id: work.id, section_id: inFlight.id },
		{ content: 'Opdater prislisten', project_id: work.id, section_id: done.id, labels: ['gennemsyn'] }
	];
	for (const task of tasks) await api('/tasks', task);

	// A saved filter, because the sidebar shows them and an empty heading reads as
	// a feature that does not work.
	await api('/filters', { name: 'Vigtigt i denne uge', query: 'p1 & 7 days' });

	return { work: work.id, house: house.id, garden: garden.id };
}
