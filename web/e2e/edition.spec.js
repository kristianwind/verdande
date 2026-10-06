import { test, expect } from '@playwright/test';

/**
 * Noter-udgaven, kørt som sit eget program.
 *
 * Egen server på sin egen port med `VERDANDE_EDITION=notes`, egen database og
 * ingen lånt session: den her instans har aldrig set den konto, den fulde udgaves
 * opsætning lavede, så den laver sin egen.
 *
 * Og det er hele grunden til at prøven findes som en separat server frem for som
 * en indstilling, en prøve skruer på. En skakt, der kun er prøvet ved at kalde en
 * funktion, er prøvet på det, nogen mente — det her måler programmet, som en
 * bruger møder det.
 */

const KONTO = {
	email: 'noter@example.dk',
	name: 'Noteskriver',
	password: 'et langt kodeord til test'
};

test.beforeEach(async ({ page }) => {
	await page.goto('/');
	// Første konto, hvis den ikke er lavet endnu. Prøverne i filen deler instans,
	// så den anden gang er det en almindelig login.
	const opret = page.getByRole('button', { name: 'Opret konto' });
	const login = page.getByRole('button', { name: 'Log ind' });
	// Ventet på, at EN af de to er der, før der spørges hvilken. Spurgt før
	// tegningen svarer begge nej, og så faldt prøven i den forkerte gren og ventede
	// tredive sekunder på en knap, der aldrig kom.
	await expect(opret.or(login)).toBeVisible();

	if (await opret.isVisible()) {
		await page.getByLabel(/Navn/).fill(KONTO.name);
		await page.getByLabel(/E-mail/).fill(KONTO.email);
		await page.getByLabel(/Adgangskode/).fill(KONTO.password);
		await opret.click();
		// Beaconens besked står øverst første gang; den er ikke det her prøvens
		// ærinde, men den dækker for det, der er.
		const besked = page.getByRole('region', { name: /melder, at den findes/ });
		if (await besked.isVisible().catch(() => false)) {
			await besked.getByRole('button', { name: 'Behold den' }).click();
		}
	} else {
		await page.getByLabel(/E-mail/).fill(KONTO.email);
		await page.getByLabel(/Adgangskode/).fill(KONTO.password);
		await page.getByRole('button', { name: 'Log ind' }).click();
	}
	await expect(page.getByRole('navigation', { name: 'Hovedmenu' })).toBeVisible();
});

test('instansen siger selv, at den er noter-udgaven', async ({ page }) => {
	// Fladen kan ikke lukke af for noget, den ikke får at vide, og den skal have
	// det at vide, før nogen er logget ind.
	const svar = await page.request.get('/api/v1/auth/setup');
	expect((await svar.json()).edition).toBe('notes');
});

test('der er ingen vej til opgaverne i menuen', async ({ page }) => {
	const menu = page.getByRole('navigation', { name: 'Hovedmenu' });
	await expect(menu).toBeVisible();

	// Udeladt, ikke gråtonet: ruterne bag dem er ikke monteret på serveren.
	for (const navn of ['I dag', 'Kommende', 'Kalender', 'Uddelegeret']) {
		await expect(menu.getByRole('link', { name: navn })).toHaveCount(0);
	}

	// Og det, udgaven ER til, står der. En menu uden opgaver OG uden noter ville
	// også bestå prøven ovenfor, og den ville være en sletning frem for en skakt.
	await expect(menu.getByRole('link', { name: 'Noter', exact: true })).toBeVisible();
});

test('forsiden er noterne, ikke en tom opgaveliste', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveURL(/\/noter$/);
});

test('en gemt adresse til en opgaveside fører til noterne i stedet for et blindt spor', async ({
	page
}) => {
	// Nogen, der har gemt et link fra en fuld instans, skal lande et sted, der
	// giver mening. Der er ikke sket noget forkert — adressen hører bare til et
	// andet program end det, der kører her.
	for (const sti of ['/upcoming', '/kalender', '/uddelegeret', '/faerdige']) {
		await page.goto(sti);
		await expect(page).toHaveURL(/\/noter$/);
	}
});

test('⌘K finder noter og ikke opgaver', async ({ page }) => {
	await page.goto('/noter');
	await page.getByRole('button', { name: 'Ny note' }).click();
	const felt = page.getByRole('textbox', { name: 'Notens tekst' });
	await expect(felt).toHaveText('');
	await felt.click();
	await page.keyboard.type('Kvartalsregnskabet');
	await felt.blur();
	await expect(page.locator('.notes button.row strong').filter({ hasText: /^Kvartalsregnskabet$/ })).toHaveCount(1);

	await page.getByRole('button', { name: /Søg/ }).click();
	const palette = page.getByRole('dialog');
	await palette.getByRole('textbox').fill('Kvartalsregnskabet');

	// Noten findes — ellers beviser det næste ingenting.
	await expect(palette.locator('li button', { hasText: 'Kvartalsregnskabet' })).toBeVisible();
	// Og søgningen på serveren svarer uden opgaver overhovedet.
	const svar = await page.request.get('/api/v1/search?q=Kvartalsregnskabet');
	expect((await svar.json()).tasks ?? []).toEqual([]);
});

test('opgaveruterne findes slet ikke på serveren', async ({ page }) => {
	// Det her er forskellen på skjult og fraværende, målt udefra.
	for (const sti of ['/api/v1/tasks', '/api/v1/labels', '/api/v1/upcoming', '/api/v1/filters']) {
		const svar = await page.request.get(sti);
		expect(svar.status(), `${sti} svarede ${svar.status()}`).toBe(404);
	}
	// Kontrollen: en rute, der ER monteret, svarer ikke 404 — ellers ville
	// ovenstående bestå på en server, der var helt væk.
	expect((await page.request.get('/api/v1/notes')).status()).toBe(200);
});
