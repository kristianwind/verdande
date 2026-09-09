/**
 * Hvilken browser et link åbner i.
 *
 * # Hvad der ikke kan lade sig gøre
 *
 * En webside kan hverken se, hvilke browsere der er installeret, eller bede
 * systemet om at åbne et link i en bestemt af dem. Listen over installerede
 * programmer er lukket land med vilje — den er et fingeraftryk, der ville følge
 * folk rundt på nettet — og et almindeligt link går altid til standardbrowseren.
 *
 * Det eneste håndtag, der findes, er de URL-skemaer, browserne selv registrerer:
 * `firefox://open-url?url=…`, `googlechrome://…`, `microsoft-edge:…`. Der er ingen
 * måde at spørge, om et skema findes; man kan kun prøve.
 *
 * # Hvad der så sker
 *
 * Vi prøver skemaet og holder øje med, om siden forsvinder. Åbnede en anden
 * browser, mister denne fane fokus, og så er der ikke mere at gøre. Sker der
 * ingenting inden for et sekund — hvilket er, hvad en Mac gør, hvor Chrome og
 * Firefox ikke registrerer noget skema — åbnes linket helt normalt.
 *
 * Faldbacket er ikke en detalje: uden det ville en indstilling, der ser ud til at
 * virke på telefonen, gøre links uklikbare på skrivebordet.
 *
 * # Hvorfor det står i localStorage
 *
 * Samme grund som sidebjælkens bredde: det er en egenskab ved maskinen, man
 * sidder ved, ikke ved personen. `firefox://` virker på telefonen og gør
 * ingenting på Mac'en, så et valg, der fulgte kontoen, ville være forkert det ene
 * af de to steder hver gang.
 */

const KEY = 'verdande:link-opener';

/**
 * De skemaer, der er værd at tilbyde, og hvad de faktisk gør.
 *
 * `{url}` erstattes af adressen. Nogle skemaer vil have den kodet som en parameter
 * (Firefox), andre sætter den bag skemaet, som den er (Chrome, Edge) — derfor er
 * det en skabelon og ikke et navn, og derfor kan man skrive sin egen.
 */
export const OPENERS = [
	{ id: '', label: 'link.openerDefault', template: '' },
	{ id: 'safari', label: 'link.openerSafari', template: 'x-safari-{url}' },
	{ id: 'chrome', label: 'link.openerChrome', template: 'googlechrome{stripped}' },
	{ id: 'firefox', label: 'link.openerFirefox', template: 'firefox://open-url?url={encoded}' },
	{ id: 'edge', label: 'link.openerEdge', template: 'microsoft-edge:{url}' }
];

/** Det gemte valg: en af id'erne ovenfor, eller en skabelon man selv har skrevet. */
export function stored() {
	if (typeof localStorage === 'undefined') return { id: '', template: '' };
	try {
		const raw = localStorage.getItem(KEY);
		if (!raw) return { id: '', template: '' };
		const parsed = JSON.parse(raw);
		return { id: parsed.id ?? '', template: parsed.template ?? '' };
	} catch {
		return { id: '', template: '' };
	}
}

export function save(choice) {
	try {
		localStorage.setItem(KEY, JSON.stringify(choice));
	} catch {
		// Privat vindue; valget holder til fanen lukkes.
	}
}

/** Skabelonen, der gælder nu — enten den valgte eller den, man selv har skrevet. */
function template() {
	const choice = stored();
	if (choice.id === 'custom') return choice.template;
	return OPENERS.find((o) => o.id === choice.id)?.template ?? '';
}

/**
 * Skabelonen fyldt ud med adressen.
 *
 * `{url}` er adressen, som den er — det er dét, `x-safari-https://…` og
 * `microsoft-edge:https://…` vil have. `{encoded}` er den som parameterværdi, til
 * Firefox. `{stripped}` er den uden `https://` foran, som Chrome vil have den:
 * `googlechrome://example.dk`.
 */
export function expand(tpl, url) {
	if (!tpl) return '';
	return tpl
		.replace('{encoded}', encodeURIComponent(url))
		.replace('{stripped}', '://' + url.replace(/^https?:\/\//, ''))
		.replace('{url}', url);
}

/**
 * Åbner en adresse i den valgte browser, og ellers helt normalt.
 *
 * Returnerer, om skemaet blev forsøgt. Kalderen skal stadig lade være med at
 * følge linket selv — det er derfor, klikket bliver stoppet, inden vi kommer
 * hertil.
 */
export function open(url) {
	const tpl = template();
	if (!tpl) {
		window.open(url, '_blank', 'noopener,noreferrer');
		return false;
	}

	let handled = false;
	// Forsvinder siden, åbnede noget andet den. Begge signaler, fordi de to
	// platforme ikke er enige om hvilket der kommer: en telefon skjuler siden, en
	// pc flytter fokus.
	const gone = () => {
		handled = true;
	};
	window.addEventListener('blur', gone, { once: true });
	document.addEventListener('visibilitychange', gone, { once: true });

	// Adresselinjen og ikke et skjult iframe.
	//
	// Iframet er den kendte opskrift, og den kan ikke bruges her: appens egen
	// Content-Security-Policy siger `default-src 'self'`, så en ramme med et
	// fremmed skema bliver blokeret, inden browseren når at se på den. Målt i
	// e2e-prøven, som fangede CSP-fejlen i konsollen — uden den ville skemaet
	// aldrig være blevet forsøgt i produktion, og indstillingen ville se ud til
	// at virke, fordi faldbacket åbnede linket alligevel.
	//
	// At sætte location på et ukendt skema afbryder navigationen frem for at
	// forlade siden: dokumentet bliver stående, og det er dét, der gør, at
	// faldbacket nedenfor stadig har en side at køre i. Nogle browsere siger til
	// om en adresse, de ikke forstår, og det er prisen for at kunne prøve.
	location.href = expand(tpl, url);

	// Kort, og det er ikke en smagssag.
	//
	// Faldbacket åbner en fane, og en browser tillader kun det et øjeblik efter et
	// klik — Chrome og Firefox regner med omtrent fem sekunder, Safari med
	// betydeligt mindre. Ventes der for længe, bliver faldbacket blokeret som en
	// pop-up, og så gør linket ingenting overhovedet. Seks hundrede millisekunder
	// er rigeligt til, at en anden browser når at tage over, og godt inden for
	// det, der stadig tæller som "fordi nogen klikkede".
	setTimeout(() => {
		window.removeEventListener('blur', gone);
		document.removeEventListener('visibilitychange', gone);
		if (handled || document.hidden) return;

		// Skemaet fandtes ikke her. Linket skal stadig åbne — alt andet ville gøre
		// en indstilling, der virker på telefonen, til ødelagte links på
		// skrivebordet.
		//
		// Uden `noopener`, og det er ikke en forglemmelse: med den flag returnerer
		// window.open **altid** null, også når fanen åbnede fint — det står i
		// specifikationen. Målt her, efter at faldbacket havde troet sig blokeret
		// hver eneste gang og navigeret den aktuelle fane væk i stedet. Håndtaget
		// er det eneste, der kan kende en blokeret fane fra en åbnet, så det skal
		// bruges, og opener ryddes bagefter.
		const tab = window.open(url, '_blank');
		if (tab) {
			try {
				tab.opener = null;
			} catch {
				// En fane på et andet domæne lader sig ikke altid røre. Fanen er
				// åbnet, og det var det, klikket handlede om.
			}
			return;
		}
		// Blev den alligevel blokeret, går vi i den fane, vi står i. Et dårligere
		// sted at ende end en ny fane, og et meget bedre sted end et link, der
		// ikke gjorde noget.
		location.href = url;
	}, 600);
	return true;
}
