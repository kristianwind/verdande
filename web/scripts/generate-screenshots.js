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
 * This one starts the real binary, creates its own two accounts, fills them with
 * the demo data, and takes the pictures. Nothing to set up and nothing to remember.
 *
 * The binary, not the dev server: the screenshots should show what ships. That
 * means building the frontend and copying it into cmd/verdande/webbuild first,
 * exactly as the smoke tests do — a capture of a stale embedded build would be a
 * picture of an app nobody is running.
 *
 * English, because the people who read the landing page and the docs read English.
 * Not by translating anything here: the account is created with an English locale
 * and the app is then in English by itself, which also means a string nobody
 * translated shows up in a picture instead of hiding until a user finds it.
 */
import { chromium } from '@playwright/test';
import { spawn, execSync } from 'node:child_process';
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const web = join(here, '..');
const repo = join(web, '..');
const dataDir = join(web, '.screenshot-data');

const PORT = 8098;
const baseURL = `http://127.0.0.1:${PORT}`;
const USER = { name: 'Kristian', email: 'kristian@example.dk', password: 'et langt kodeord' };
// Somebody to share with. Half of what the app is for only exists once there are
// two people in it: an assignee on a task, a name in the history, a face in the
// members list, and "Waiting on others" as something other than an empty page.
const COLLEAGUE = { name: 'Mette', email: 'mette@example.dk', password: 'et andet langt kodeord' };

// 1400×900 is what the landing page's <img> declares. A capture at another size
// would be resampled by the browser and look soft next to the text beside it.
const WIDTH = 1400;
const HEIGHT = 900;

// The docs and the README read the PNGs; the landing page reads the WebP, which is
// a fifth of the weight over the wire. Both are written from the same capture, so
// the two sets can never drift apart.
const PNG_DIR = join(repo, 'docs', 'screenshots');
const WEBP_DIR = join(repo, 'site', 'screenshots');

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
	// By address rather than by clicking the first row: the drawer has to be over
	// the one task that was given a description, sub-tasks, a file, a reminder and a
	// conversation, or the picture shows an empty form and proves nothing.
	{ name: 'detail', path: (ids) => `/opgave/${ids.detail}`, theme: 'dark' },
	{ name: 'notes', path: '/noter', theme: 'dark', prepare: openMeetingNote },
	{ name: 'notes-source', path: '/noter', theme: 'dark', prepare: showNoteSource },
	{ name: 'delegated', path: '/uddelegeret', theme: 'dark' },
	{ name: 'settings', path: '/indstillinger/integrationer', theme: 'dark' },
	// Scrolled, because the themes are the second panel on the page and a picture
	// captioned "five themes" that shows the profile form is a caption about
	// nothing.
	{ name: 'themes', path: '/indstillinger', theme: 'dark', prepare: showThemes },
	{ name: 'admin', path: '/indstillinger/brugere', theme: 'dark' },
	{ name: 'history', path: '/indstillinger/historik', theme: 'dark' }
];

const NOTE_TITLE = 'Launch meeting — agenda';

async function showList(page) {
	await page.getByRole('button', { name: 'List', exact: true }).click();
	await page.waitForTimeout(600);
}

async function showMonth(page) {
	await page.getByRole('button', { name: 'Month', exact: true }).click();
	await page.waitForTimeout(800);
}

async function showWeek(page) {
	await page.getByRole('button', { name: 'Week', exact: true }).click();
	await page.waitForTimeout(800);
}

async function openMeetingNote(page) {
	// By text inside the row rather than by its accessible name: a row's name is its
	// title, its date, the first line of its body and the project it is filed in,
	// all run together.
	await page.locator('button.row', { hasText: NOTE_TITLE }).first().click();
	await page.waitForTimeout(800);
}

async function showNoteSource(page) {
	await openMeetingNote(page);
	await page.getByRole('button', { name: 'Show as code' }).click();
	await page.waitForTimeout(600);
}

async function showThemes(page) {
	// scrollIntoView rather than Playwright's scrollIntoViewIfNeeded: the heading is
	// already a few pixels inside the viewport when the page loads, so "if needed"
	// decides nothing is needed and the picture is of the profile form above it.
	//
	// `.first()`, because the looks below the themes carry the same heading — one
	// panel, two questions, and both of them called Appearance.
	await page
		.getByRole('heading', { name: 'Appearance' })
		.first()
		.evaluate((el) => el.closest('section').scrollIntoView({ block: 'start' }));
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

	// The browser's own language, which is not the same setting as the context's.
	// `<input type="date">` is drawn by Chromium, not by the app, and it takes its
	// order from this one — so without it the drawer shows an American date inside
	// a day-month app, which is the exact thing i18n.svelte.js warns about.
	browser = await chromium.launch({ args: ['--lang=en-GB'] });
	const context = await browser.newContext(viewing());
	const page = await context.newPage();

	await page.goto(baseURL);
	// The sign-up form is the one screen that is always Danish: it is drawn before
	// there is an account to read a language off, and Danish is the fallback. So
	// these labels are Danish on purpose, and the app is English from the moment
	// the account exists — which is every screen this script photographs.
	await page.getByLabel(/Navn/).fill(USER.name);
	await page.getByLabel(/E-mail/).fill(USER.email);
	await page.getByLabel(/Adgangskode/).fill(USER.password);
	await page.getByRole('button', { name: 'Opret konto' }).click();
	await page.getByRole('navigation', { name: 'Main menu' }).waitFor();

	console.log('seeding…');
	const ids = await page.evaluate(seed, { colleague: COLLEAGUE.email, noteTitle: NOTE_TITLE });

	console.log('adding the second person…');
	const ids2 = await addColleague(browser, ids);
	Object.assign(ids, ids2);
	await page.reload();
	await page.getByRole('navigation', { name: 'Main menu' }).waitFor();

	mkdirSync(PNG_DIR, { recursive: true });
	mkdirSync(WEBP_DIR, { recursive: true });

	// A page of its own for the WebP encoding, on about:blank: the app's pages send
	// a content security policy, and an encoder that borrows whichever page was last
	// photographed would be one policy change away from silently producing nothing.
	const encoder = await context.newPage();
	await encoder.goto('about:blank');

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
		writeFileSync(join(PNG_DIR, `${shot.name}.png`), png);
		writeFileSync(join(WEBP_DIR, `${shot.name}.webp`), await toWebP(encoder, png));
		console.log(`  ${shot.name}`);
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

/** The browser settings every context here shares. */
function viewing() {
	return {
		viewport: { width: WIDTH, height: HEIGHT },
		// The locale is not a formatting preference: the sign-up form sends
		// navigator.language as the account's language, so this is what makes the
		// interface, the Inbox's name and every date English. The zone is pinned for
		// the same reason the smoke tests pin it — every due date is resolved in it.
		locale: 'en-GB',
		timezoneId: 'Europe/Copenhagen',
		// Dark, because that is what the shots are forced to below. app.html reads
		// prefers-color-scheme before first paint, and Playwright's default is light
		// — which left the settings page rendering dark with the *light* theme
		// ringed as the chosen one.
		colorScheme: 'dark',
		deviceScaleFactor: 2
	};
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
 * Encodes a PNG as WebP, in the browser that is already open.
 *
 * Chromium encodes WebP itself, so the alternative — shelling out to `cwebp` —
 * would be something to install first, in a script whose whole point is that there
 * is nothing to install. Resized on the way through: the landing page shows these
 * 1400 CSS pixels wide, and a full-resolution WebP is twice the bytes for detail
 * no screen renders.
 */
async function toWebP(encoder, png) {
	const base64 = await encoder.evaluate(
		async ({ data, width, quality }) => {
			const image = new Image();
			image.src = `data:image/png;base64,${data}`;
			await image.decode();

			const canvas = document.createElement('canvas');
			canvas.width = width;
			canvas.height = Math.round((image.naturalHeight / image.naturalWidth) * width);
			const ctx = canvas.getContext('2d');
			// The default resampler leaves the interface's hairlines ragged, which on a
			// screenshot of a screenshot is the first thing the eye finds.
			ctx.imageSmoothingQuality = 'high';
			ctx.drawImage(image, 0, 0, canvas.width, canvas.height);
			return canvas.toDataURL('image/webp', quality).split(',')[1];
		},
		// 0.75 is cwebp's default and where these stop getting smaller for anything
		// the eye finds: 0.85 is a fifth more bytes for a difference that survives
		// only under magnification, and the landing page's first image is the one
		// somebody waits for.
		{ data: png.toString('base64'), width: 1800, quality: 0.75 }
	);
	return Buffer.from(base64, 'base64');
}

/**
 * Signs the second person up through the invite the seed left behind, has them say
 * something, and hands their work back to the first.
 *
 * A context of its own because a session is a cookie: signing up in the page the
 * screenshots are taken from would replace the account they are taken as.
 */
async function addColleague(browser, ids) {
	const context = await browser.newContext(viewing());
	const page = await context.newPage();
	try {
		await page.goto(ids.invite);
		// Danish labels again, and for the same reason: nobody is signed in yet.
		await page.getByLabel(/Navn/).fill(COLLEAGUE.name);
		await page.getByLabel(/Adgangskode/).fill(COLLEAGUE.password);
		await page.getByRole('button', { name: 'Opret konto' }).click();
		await page.getByRole('navigation', { name: 'Main menu' }).waitFor();
		return await page.evaluate(handOver, ids);
	} finally {
		await context.close();
	}
}

/**
 * What the second person does, from inside their own session: answer the task they
 * were opened on, and take the three they are being waited for.
 *
 * The assignments are made here rather than by the owner because an editor may
 * assign, and doing it from this side proves it. Written as one function because it
 * is passed to the browser as source.
 */
async function handOver(ids) {
	const api = async (path, body, method = 'POST') => {
		const r = await fetch(`/api/v1${path}`, {
			method,
			headers: { 'Content-Type': 'application/json' },
			body: body ? JSON.stringify(body) : undefined
		});
		if (!r.ok) throw new Error(`${method} ${path} → ${r.status}`);
		return r.status === 204 ? null : r.json();
	};

	const me = await api('/auth/me', null, 'GET');
	for (const id of ids.delegate) await api(`/tasks/${id}`, { assignee_id: me.id }, 'PATCH');

	await api(`/tasks/${ids.detail}/comments`, {
		body: 'Room is booked for ten. Summer numbers are in the shared folder — the labels are still with the printer, I chase them Thursday.'
	});
	return { colleague: me.id };
}

/**
 * The demo data, created through the app's own API from inside the page.
 *
 * From the page rather than from node, so the session cookie and the same-origin
 * header the API insists on come along without being reconstructed here. Written
 * as one function because it is passed to the browser as source.
 *
 * Ordinary English household and work tasks, the same shape the Danish set had.
 * Screenshots of "Task 1, Task 2, Task 3" show the layout and hide whether the app
 * is any good at the thing it is for.
 */
async function seed({ colleague, noteTitle }) {
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
	const work = await api('/project-groups', { name: 'Work' });
	const home = await api('/project-groups', { name: 'Home' });
	await api(`/project-groups/${work.id}`, { color: 'indigo' }, 'PATCH');
	await api(`/project-groups/${home.id}`, { color: 'green' }, 'PATCH');

	const launch = await api('/projects', {
		name: 'Autumn launch',
		color: 'indigo',
		view_mode: 'board',
		group_id: work.id
	});
	const house = await api('/projects', { name: 'House', color: 'amber', group_id: home.id });
	const garden = await api('/projects', { name: 'Garden', color: 'green', group_id: home.id });

	const planning = await api(`/projects/${launch.id}/sections`, { name: 'Planning' });
	const inFlight = await api(`/projects/${launch.id}/sections`, { name: 'In progress' });
	const review = await api(`/projects/${launch.id}/sections`, { name: 'Up for review' });

	const tasks = [
		// One thing that slipped. Today with an overdue band on it is what a Thursday
		// actually looks like, and it is the only way to photograph the band at all.
		{
			content: 'Chase the printer about the labels',
			due_date: day(-2),
			priority: 2,
			project_id: launch.id,
			section_id: inFlight.id,
			labels: ['waiting']
		},

		// Today, in the Inbox and across the projects: the Today view is the first
		// picture and it has to look like a real day.
		{ content: 'Call the roofer about the ridge tiles', due_date: day(0), priority: 1, project_id: house.id },
		{
			content: 'Write the opening notes for the launch meeting',
			due_date: day(0),
			priority: 2,
			project_id: launch.id,
			section_id: planning.id,
			duration_min: 90,
			labels: ['meeting']
		},
		{ content: 'Drop the bike off for its service', due_date: day(0), priority: 3, labels: ['errands'] },
		{ content: 'Water the greenhouse', due_date: day(0), recurrence_rule: 'FREQ=DAILY', project_id: garden.id },
		{ content: 'Collect the parcel from the post office', due_date: day(0), labels: ['errands'] },

		// The next couple of weeks, so Upcoming and the month grid have something in
		// them rather than a row of empty cells.
		{ content: 'Book a plumber for the bathroom', due_date: day(1), priority: 2, project_id: house.id },
		{ content: 'Cut the beech hedge back', due_date: day(2), project_id: garden.id },
		{ content: 'Go through the budget', due_date: day(3), priority: 1, project_id: launch.id, section_id: inFlight.id },
		{ content: 'Order bulbs for the autumn planting', due_date: day(5), project_id: garden.id },
		{ content: 'Swap the winter tyres on', due_date: day(8), priority: 3 },
		{ content: 'Send the minutes round', due_date: day(9), project_id: launch.id, section_id: review.id },
		{ content: 'Paint the window sills', due_date: day(12), project_id: house.id, priority: 2 },
		{ content: 'Sow winter herbs in the cold frame', due_date: day(15), project_id: garden.id },
		{ content: 'Quarterly review', due_date: day(21), priority: 1, project_id: launch.id, section_id: planning.id },
		{ content: 'Clear the gutters before the autumn rain', due_date: day(26), project_id: house.id },

		// A few without dates, so the board has cards that are not all deadlines.
		{ content: 'Find a new packaging supplier', project_id: launch.id, section_id: planning.id, priority: 3 },
		{ content: 'Write the job advert', project_id: launch.id, section_id: inFlight.id },
		{ content: 'Update the price list', project_id: launch.id, section_id: review.id, labels: ['review'] }
	];
	const made = {};
	for (const task of tasks) made[task.content] = await api('/tasks', task);

	// A saved filter, because the sidebar shows them and an empty heading reads as
	// a feature that does not work.
	await api('/filters', { name: 'Important this week', query: 'p1 & 7 days' });

	// The one task that is photographed open. Everything the drawer can hold, so the
	// picture answers "what is in there" rather than showing an empty form.
	const detail = made['Write the opening notes for the launch meeting'];
	await api(
		`/tasks/${detail.id}`,
		{
			description:
				'Fifteen minutes, no slides. What shipped over the summer, what is left, and what we are deliberately not doing before the first.'
		},
		'PATCH'
	);
	for (const content of [
		'Pull the numbers from last season',
		'Ask Mette for the packaging quotes',
		'Book the room'
	]) {
		await api('/tasks', { content, parent_id: detail.id, project_id: launch.id });
	}
	await api(`/tasks/${detail.id}/reminders`, { offset_min: 60 });
	await api(`/tasks/${detail.id}/comments`, {
		body: 'Keeping it to three points. Anything else goes on the list for the week after.'
	});

	// A file on the task, because "comments, attachments" is a claim and an empty
	// Files heading is the opposite of proof.
	const form = new FormData();
	form.append('file', new Blob([new Uint8Array(9400)], { type: 'application/pdf' }), 'packaging-quote.pdf');
	await fetch(`/api/v1/tasks/${detail.id}/attachments`, { method: 'POST', body: form });

	// The notes, newest last: the list opens on what was touched most recently, so
	// the one the screenshots open is the one written last.
	const notes = [
		{
			body: `# Reading list

- *Shape Up* — the six-week bit, not the pitch bit
- The SQLite WAL paper, again
- Anything at all about pricing`
		},
		{
			project_id: garden.id,
			body: `# The bulb order

Two hundred, split three ways, in before the first frost:

1. Narcissus along the south wall
2. Alliums in the bed by the shed
3. Crocus wherever there is room

The bed by the shed is heavy — grit in the hole, or they rot. #Garden`
		},
		{
			body: `# What the roofer said

Ridge tiles have moved, maybe a metre of them. Not urgent, not something to leave
over a winter either.

- Quote by the end of the month
- Two days' work, scaffold on the north side
- Ask whether the gutters can be done at the same time

#House`
		},
		{
			project_id: launch.id,
			body: `# Price list — what changes

Nothing has been looked at since March.

- 250 g bag: **79 → 85**
- 1 kg bag: **265 → 279**
- Subscription: **199 → 209**

Subscribers keep the old price for three months. That is the whole of the goodwill
budget and it is worth it.`
		},
		{
			pinned: true,
			project_id: launch.id,
			body: `# ${noteTitle}

Thursday, fifteen minutes, no slides.

## What shipped over the summer

- The new packaging, minus the labels
- A CalDAV feed nobody asked for and everybody uses

## What is left

- Labels. Still with the printer, chased on Thursday
- The price list: [[Price list — what changes]]

> If it is not on the list by Friday it is not in the launch.

The build we cut the release from:

\`\`\`bash
cd web && npm run build && cd ..
go build -tags embedweb -o verdande ./cmd/verdande
\`\`\`

Room is booked. Coffee is **not** my job this week.

The opening notes are a task, so they can be ticked off: task:${detail.id}`
		}
	];
	for (const note of notes) await api('/notes', note);

	// Shared with somebody, and an invite left standing to the instance itself: two
	// of the three states the members page has, and the one it never showed before.
	const invite = await api(`/projects/${launch.id}/invites`, { email: colleague, role: 'editor' });
	await api('/users', { email: 'anders@example.dk' });

	return {
		work: launch.id,
		detail: detail.id,
		invite: invite.link,
		delegate: [
			made['Send the minutes round'].id,
			made['Update the price list'].id,
			made['Find a new packaging supplier'].id
		]
	};
}
