import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { SETTINGS, findSettings } from '../src/lib/settingsindex.js';

/**
 * Indekset over indstillingerne skal passe på siderne.
 *
 * Et indeks ved siden af det, det beskriver, er en kopi, og en kopi driver. Den
 * dag et afsnit flyttes til en anden fane eller får et nyt anker, holder
 * søgningen op med at kunne finde det — og det fejler ikke: man får bare ingen
 * træffere, hvilket ligner en søgning, der virker, på noget der ikke findes.
 *
 * Derfor læses filerne her frem for at blive klikket igennem. Halvdelen af
 * afsnittene vises kun under en betingelse — en kalenderkonto, en afventende
 * invitation, en administrator — så en prøve, der leder efter dem i en browser,
 * ville melde fejl på det, der med rette var skjult.
 */

const here = dirname(fileURLToPath(import.meta.url));
const routes = join(here, '..', 'src', 'routes');
const locales = join(here, '..', 'src', 'lib', 'locales');

/** `/indstillinger/data` → filen, den side tegnes af. */
function pageFile(path) {
	return join(routes, path, '+page.svelte');
}

test('hvert afsnit i indekset findes på den side, det peger på', () => {
	const missing = [];
	for (const entry of SETTINGS) {
		const src = readFileSync(pageFile(entry.path), 'utf8');
		const hits = src.split(`id="${entry.anchor}"`).length - 1;
		if (hits !== 1) missing.push(`${entry.path}#${entry.anchor}: ${hits} steder, ventede 1`);
	}
	expect(missing).toEqual([]);
});

test('to afsnit deler ikke et anker', () => {
	// Ankeret slås op med getElementById, som vælger vilkårligt mellem to ens. Det
	// er kun et problem inden for den samme side, så nøglen er sti plus anker.
	const seen = new Set();
	const dupes = [];
	for (const entry of SETTINGS) {
		const key = `${entry.path}#${entry.anchor}`;
		if (seen.has(key)) dupes.push(key);
		seen.add(key);
	}
	expect(dupes).toEqual([]);
});

test('overskrifterne, indekset peger på, findes på begge sprog', () => {
	const missing = [];
	for (const locale of ['da', 'en']) {
		const src = readFileSync(join(locales, `${locale}.js`), 'utf8');
		for (const entry of SETTINGS) {
			for (const key of [entry.key, entry.tab]) {
				if (!src.includes(`'${key}':`)) missing.push(`${locale}: ${key}`);
			}
		}
	}
	expect(missing).toEqual([]);
});

test('søgningen finder det, man ville skrive', () => {
	// t() er her den, der ikke oversætter: prøven skal måle indekset og ikke
	// sprogfilen, og de tekniske ord er dem, der bærer en søgning uanset sprog.
	const t = (key) => key;
	const found = (q, admin = true) =>
		findSettings(q, { t, isAdmin: admin }).map((hit) => `${hit.path}#${hit.anchor}`);

	expect(found('beacon')).toContain('/indstillinger/data#beacon');
	expect(found('webhook')).toContain('/indstillinger/integrationer#krog');
	expect(found('imap')).toContain('/indstillinger/integrationer#postkasser');
	expect(found('2fa')).toContain('/indstillinger#to-faktor');
	expect(found('ical')).toContain('/indstillinger/integrationer#kalenderfeed');

	// Tomt felt giver ingenting frem for alt: en liste over seksogtredive afsnit
	// er ikke et svar på et spørgsmål, nogen endnu ikke har stillet.
	expect(found('')).toEqual([]);
	expect(found('   ')).toEqual([]);

	// Det, der kun er en administrators, må ikke kunne findes af andre. Siden
	// afviser dem, og en træffer, der fører til en 403, er et løfte, fladen ikke
	// kan holde.
	expect(found('backup', false)).toEqual([]);
	expect(found('backup', true)).toContain('/indstillinger/data#sikkerhedskopier');
});
