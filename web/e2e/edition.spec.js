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

/**
 * Navnet.
 *
 * To udgaver af ét program er to produkter, og det eneste sted en læser afgør
 * hvilket af dem de ser på, er navnet: fanen, mærket i menuen, og det navn appen
 * lægger sig under på en hjemmeskærm. De tre står i filer, browseren læser FØR
 * noget JavaScript kører, så serveren skriver dem om på vejen ud — og derfor skal
 * de måles her, på det der faktisk blev sendt, frem for i en funktion.
 */
test('fanen, mærket og manifestet siger alle urd', async ({ page }) => {
	// Der er TO titler, og det blev opdaget her frem for i en Go-prøve: skallens,
	// som browseren læser før noget JavaScript kører og som serveren skriver om,
	// og rutens egen, som appen sætter i <svelte:head> når siden tegner. Den sidste
	// overskriver den første — så en prøve, der kun læste den ene, ville være grøn
	// over en fane, der sagde "Noter · verdande".
	const skal = await (await page.request.get('/')).text();
	expect(skal, 'skallens titel, før appen kører').toContain('<title>urd</title>');
	await expect(page, 'rutens egen titel, efter appen har tegnet').toHaveTitle(/· urd$/);

	const mærke = page.getByRole('navigation', { name: 'Hovedmenu' }).locator('.brand');
	await expect(mærke.locator('.name')).toHaveText('urd');
	// ᚢ uruz, det bogstav navnet begynder med i den ældre futhark — og ikke ᚹ
	// wunjo, som er Verdandes. Begge er navngivne værdier frem for "der er en
	// rune": en tom span og den forkerte rune ser ens ud for en prøve, der kun
	// spørger om der står noget.
	await expect(mærke.locator('.rune')).toHaveText('ᚢ');

	const manifest = await (await page.request.get('/manifest.webmanifest')).json();
	expect(manifest.name).toBe('urd');
	expect(manifest.short_name).toBe('urd');
});

test('manifestet bliver sendt som et manifest, ikke som tekst', async ({ page }) => {
	// Go kender ikke .webmanifest, så uden en eksplicit type sniffes den til
	// text/plain — og en browser ignorerer så manifestet uden at sige noget.
	// Symptomet er at installationsknappen bare ikke er der.
	const svar = await page.request.get('/manifest.webmanifest');
	expect(svar.headers()['content-type']).toContain('application/manifest+json');
});

test('omdøbningen rørte ikke lagernøglerne', async ({ page }) => {
	// Fælden i hele øvelsen: produktnavnet og localStorage-præfikset er samme ord
	// i app.html, og de er ikke samme ting. Omdøbes præfikset her og ikke i
	// resten af appen, bliver temaet sat fra én nøgle og læst fra en anden, og det
	// eneste symptom er det hvide blink, det indlejrede script findes for at
	// undgå. Målt på de bytes der blev sendt, ikke på kilden.
	const skal = await (await page.request.get('/')).text();
	expect(skal).toContain("localStorage.getItem('verdande:theme')");
	expect(skal).toContain("localStorage.getItem('verdande:look')");
	expect(skal).not.toContain('urd:theme');
});

test('navnet står også i teksterne, ikke kun i mærket', async ({ page }) => {
	// `{product}`-pladsholderen, hele vejen gennem i18n og ud på skærmen. Udseendet
	// "som du kender det" er opkaldt efter programmet, så det er det kort, der
	// skifter navn med udgaven.
	await page.goto('/indstillinger');
	await expect(page.getByRole('button', { name: /^Urd\b/ })).toBeVisible();
	await expect(page.getByRole('button', { name: /^Verdande\b/ })).toHaveCount(0);
});
