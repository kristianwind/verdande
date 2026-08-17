import { test as setup, expect } from '@playwright/test';

/**
 * Creates the first account and saves the session for every other test.
 *
 * This is also the sign-in smoke test: if the bootstrap form, the session cookie
 * or the app shell is broken, nothing below runs and the failure names this file.
 */

export const USER = {
	name: 'Kristian',
	email: 'test@example.dk',
	password: 'et langt kodeord til test'
};

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

	await page.context().storageState({ path: AUTH_FILE });
});
