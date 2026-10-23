import { test, expect } from '@playwright/test';
import { USER } from './user.js';

/**
 * The interface the way the iOS app runs it: from a different origin than the
 * server, with no cookie, its session as a bearer token.
 *
 * The real app is served from capacitor://localhost, which a test browser cannot
 * open. `app.localhost` stands in for it — Chromium sends every *.localhost to
 * the loopback address, so it is this same server, but to the browser it is a
 * different origin, and that difference is the whole thing under test. The server
 * is told to treat it as the app's origin (VERDANDE_APP_ORIGINS in the config),
 * and a stub of Capacitor's bridge makes the interface believe it is in the app.
 *
 * bypassCSP because the server's policy is written for its own pages
 * (`connect-src 'self'`); the app's pages are inside the app and never carry it.
 * Without this the browser would block exactly the cross-origin calls the test
 * exists to make, and the failure would read as the app not working.
 */

const PORT = 8097;
const SERVER = `http://localhost:${PORT}`;

test.use({
	baseURL: `http://app.localhost:${PORT}`,
	bypassCSP: true,
	storageState: { cookies: [], origins: [] }
});

test.beforeEach(async ({ page }) => {
	await page.addInitScript(() => {
		window.Capacitor = { isNativePlatform: () => true };
	});
});

/** The app's session, read the way the app reads it. */
const storedToken = (page) =>
	page.evaluate(() => localStorage.getItem('verdande.app.session') ?? '');

test('appen vælger sin server, logger ind med et token og følger med live', async ({ page }) => {
	await page.goto('/');

	// First the address — and an address with nothing behind it is said to be so,
	// rather than leading to a sign-in form that cannot succeed.
	await expect(page.getByText('Hvilken Verdande vil du logge ind på?')).toBeVisible();
	await page.getByLabel(/Serveradresse/).fill('http://localhost:1');
	await page.getByRole('button', { name: 'Fortsæt' }).click();
	await expect(page.getByText(/Kunne ikke nå localhost:1/)).toBeVisible();

	await page.getByLabel(/Serveradresse/).fill(SERVER);
	await page.getByRole('button', { name: 'Fortsæt' }).click();

	// The sign-in form, saying where it signs in to.
	await expect(page.getByText(`localhost:${PORT}`)).toBeVisible();
	await page.getByLabel(/E-mail/).fill(USER.email);
	await page.getByLabel(/Adgangskode/).fill(USER.password);
	await page.getByRole('button', { name: 'Log ind', exact: true }).click();
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();

	// Signed in by token, and by nothing else: no session cookie on either origin.
	const token = await storedToken(page);
	expect(token).not.toBe('');
	const cookies = await page.context().cookies();
	expect(cookies.filter((c) => c.name.includes('verdande_session'))).toEqual([]);

	// Live sync over a ticketed socket: a project made behind the interface's back
	// — straight to the API, as another device would — shows up in the sidebar
	// without a reload. Nothing but the socket can have put it there.
	const name = `Fra en anden enhed ${Date.now()}`;
	const status = await page.evaluate(
		async ({ server, token, name }) => {
			const res = await fetch(`${server}/api/v1/projects`, {
				method: 'POST',
				headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
				body: JSON.stringify({ name })
			});
			return res.status;
		},
		{ server: SERVER, token, name }
	);
	expect(status).toBe(201);
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' }).getByText(name)).toBeVisible();

	// A picture from the server, which an <img> cannot fetch with a token. It is
	// shown from a blob, and the path it came from is kept for the note to save.
	const path = await page.evaluate(
		async ({ server, token }) => {
			const auth = { Authorization: `Bearer ${token}` };
			const note = await fetch(`${server}/api/v1/notes`, {
				method: 'POST',
				headers: { ...auth, 'Content-Type': 'application/json' },
				body: JSON.stringify({ body: '# Billede fra appen' })
			}).then((r) => r.json());
			// A one-pixel PNG.
			const png = Uint8Array.from(
				atob(
					'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg=='
				),
				(c) => c.charCodeAt(0)
			);
			const form = new FormData();
			form.append('file', new Blob([png], { type: 'image/png' }), 'prik.png');
			const saved = await fetch(`${server}/api/v1/notes/${note.id}/attachments`, {
				method: 'POST',
				headers: auth,
				body: form
			}).then((r) => r.json());
			const img = document.createElement('img');
			img.id = 'appbillede';
			img.setAttribute('src', saved.url);
			document.body.append(img);
			return saved.url;
		},
		{ server: SERVER, token }
	);
	const img = page.locator('#appbillede');
	await expect(img).toHaveAttribute('data-src', path);
	await expect(img).toHaveAttribute('src', /^blob:/);
	await expect.poll(() => img.evaluate((el) => el.naturalWidth)).toBe(1);

	// Signing out takes the token off the phone, and keeps the server.
	await page.evaluate(() => document.getElementById('appbillede')?.remove());
	// Two taps: the first asks, the second signs out.
	await page.getByRole('button', { name: /Log ud/ }).click();
	await page.getByRole('button', { name: /Tryk igen for at logge ud/ }).click();
	await expect(page.getByText(`localhost:${PORT}`)).toBeVisible();
	expect(await storedToken(page)).toBe('');

	// And the token is dead on the server too, not just forgotten here.
	const after = await page.evaluate(
		async ({ server, token }) =>
			(await fetch(`${server}/api/v1/auth/me`, { headers: { Authorization: `Bearer ${token}` } }))
				.status,
		{ server: SERVER, token }
	);
	expect(after).toBe(401);
});

/**
 * Signs in the way the app does, outside the page, and hands the page the server
 * and the session as the app would have stored them.
 */
async function signedInApp(page) {
	const origin = { Origin: `http://app.localhost:${PORT}` };
	const res = await fetch(`${SERVER}/api/v1/auth/login`, {
		method: 'POST',
		headers: { ...origin, 'Content-Type': 'application/json' },
		body: JSON.stringify({
			email: USER.email,
			password: USER.password,
			client: 'app',
			device_name: 'Prøve-iPhone'
		})
	});
	const { session_token: token } = await res.json();
	expect(token).toBeTruthy();
	await page.addInitScript(
		({ server, token }) => {
			localStorage.setItem('verdande.app.server', server);
			localStorage.setItem('verdande.app.session', token);
		},
		{ server: SERVER, token }
	);
	const call = async (method, path, body) => {
		const init = { method, headers: { ...origin, Authorization: `Bearer ${token}` } };
		if (body instanceof FormData) init.body = body;
		else if (body !== undefined) {
			init.headers['Content-Type'] = 'application/json';
			init.body = JSON.stringify(body);
		}
		const r = await fetch(`${SERVER}/api/v1${path}`, init);
		return r.status === 204 ? null : r.json();
	};
	return call;
}

/**
 * The picture in a note is shown from a blob made on the phone. Editing the note
 * turns the page back into markdown, and what it must write is the path the
 * picture came from — a blob address saved into a note is a picture that is gone
 * the next time anybody opens it, on any device.
 */
test('en note med et billede gemmer stien, ikke blob-adressen, når den redigeres i appen', async ({
	page
}) => {
	const call = await signedInApp(page);

	const note = await call('POST', '/notes', { body: '# Billednote fra appen' });
	const png = Buffer.from(
		'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAwS2OUAAAAABJRU5ErkJggg==',
		'base64'
	);
	const form = new FormData();
	form.append('file', new Blob([png], { type: 'image/png' }), 'prik.png');
	const saved = await call('POST', `/notes/${note.id}/attachments`, form);
	await call('PATCH', `/notes/${note.id}`, {
		body: `# Billednote fra appen\n\n![](${saved.url})\n\nTekst under billedet`
	});

	await page.goto('/noter?note=' + note.id);
	const ed = page.getByRole('textbox', { name: 'Notens tekst' });
	const img = ed.locator('img');
	await expect(img).toHaveAttribute('src', /^blob:/);
	await expect(img).toHaveAttribute('data-src', saved.url);

	// Touch the note so it saves, and wait for the save itself.
	const patched = page.waitForResponse(
		(r) => r.request().method() === 'PATCH' && /\/notes\/[^/]+$/.test(new URL(r.url()).pathname) && r.ok()
	);
	await ed.getByText('Tekst under billedet').click();
	await page.keyboard.press('End');
	await page.keyboard.type(' og mere');
	await patched;

	const after = await call('GET', `/notes/${note.id}`);
	expect(after.body).toContain('og mere');
	expect(after.body).toContain(`](${saved.url})`);
	expect(after.body).not.toContain('blob:');
});
