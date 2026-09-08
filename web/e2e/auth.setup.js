import { test as setup, expect } from '@playwright/test';

/**
 * Creates the first account and saves the session for every other test.
 *
 * This is also the sign-in smoke test: if the bootstrap form, the session cookie
 * or the app shell is broken, nothing below runs and the failure names this file.
 */

import { USER } from './user.js';

const AUTH_FILE = 'e2e/.auth/user.json';

setup('opret den første konto og log ind', async ({ page }) => {
	await page.goto('/');

	await expect(page.getByText('Opret den første konto')).toBeVisible();

	// Regexes, not exact strings: the password label carries a hint beside it, so
	// its accessible name is the label plus that sentence.
	await page.getByLabel(/Navn/).fill(USER.name);
	await page.getByLabel(/E-mail/).fill(USER.email);
	await page.getByLabel(/Adgangskode/).fill(USER.password);
	await page.getByRole('button', { name: 'Opret konto' }).click();

	// The shell, not just a redirect: a 200 that renders nothing would pass a
	// URL assertion and fail a person.
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'I dag' })).toBeVisible();

	// Beaconet er slået til som udgangspunkt, og det bliver sagt uopfordret her —
	// det er hele grunden til, at et default-on er til at forsvare. Prøvet her,
	// fordi det er det eneste sted, en helt frisk installation bliver logget ind
	// på for første gang, og fordi beskeden ellers ville stå i vejen for hver
	// eneste prøve nedenfor og skulle klikkes væk uden at nogen havde set efter,
	// om den overhovedet sagde det rigtige.
	const notice = page.getByRole('region', { name: /melder, at den findes/ });
	await expect(notice).toBeVisible();
	// Værdierne står i beskeden, ikke ordet "anonymt": et løfte om telemetri er
	// præcis så meget værd som læserens mulighed for at efterprøve det.
	await expect(notice.getByText('instance_id')).toBeVisible();
	await expect(notice.getByText('version')).toBeVisible();
	await notice.getByRole('button', { name: 'Behold den' }).click();
	await expect(notice).toBeHidden();

	await page.context().storageState({ path: AUTH_FILE });
});
