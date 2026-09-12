/**
 * Hvor hver indstilling står, så den kan findes ved at søge efter den.
 *
 * Indstillingerne er ti sider med en snes afsnit i alt, og der var ingen vej til
 * et bestemt af dem andet end at huske, hvilken fane det lå under. ⌘K kunne
 * opgaver, projekter og noter — altså alt det, man selv har skrevet, og intet af
 * det, programmet består af.
 *
 * Ét indeks og ikke to søgninger. Feltet øverst i indstillingerne og paletten
 * stiller det samme spørgsmål, og to lister over de samme afsnit ville være to
 * steder at glemme et nyt et.
 *
 * Overskrifterne står ikke her. Hver post peger på den nøgle, afsnittets egen
 * overskrift allerede bruger, så et afsnit, der bliver omdøbt, følger med af sig
 * selv — og søgningen kan begge sprog uden at kende nogen af dem.
 *
 * `words` er derimod skrevet her, og det er med vilje: det er søgeord, ingen får
 * at se. De er de tekniske navne — `imap`, `webcal`, `2fa`, `webhook` — som er de
 * samme på dansk og engelsk, og som er dem, man taster, når man leder efter det,
 * man kender andetsteds fra. Det, der skal oversættes, er overskriften, og den
 * ligger i sprogfilen.
 */

/** Hver post: siden, ankeret på den, overskriftens nøgle, fanens nøgle. */
export const SETTINGS = [
	// Konto
	{ path: '/indstillinger', anchor: 'profil', key: 'account.profile', tab: 'settings.tab.account', words: 'profil profile navn name email mail tidszone timezone sprog language' },
	{ path: '/indstillinger', anchor: 'udseende', key: 'account.appearance', tab: 'settings.tab.account', words: 'tema theme dark light farve color' },
	{ path: '/indstillinger', anchor: 'look', key: 'account.look', tab: 'settings.tab.account', words: 'serif terminal' },
	{ path: '/indstillinger', anchor: 'stoerrelser', key: 'account.sizes', tab: 'settings.tab.account', words: 'skrift font zoom' },
	{ path: '/indstillinger', anchor: 'links', key: 'link.title', tab: 'settings.tab.account', words: 'browser safari chrome firefox' },
	{ path: '/indstillinger', anchor: 'adgangskode', key: 'account.password', tab: 'settings.tab.account', words: 'password kodeord' },
	{ path: '/indstillinger', anchor: 'to-faktor', key: 'account.totp', tab: 'settings.tab.account', words: '2fa totp authenticator to-faktor' },
	{ path: '/indstillinger', anchor: 'passkey', key: 'passkey.title', tab: 'settings.tab.account', words: 'passkey webauthn touch id face' },
	{ path: '/indstillinger', anchor: 'enheder', key: 'account.devices', tab: 'settings.tab.account', words: 'session sessioner devices log ud' },
	{ path: '/indstillinger', anchor: 'stoette', key: 'account.support', tab: 'settings.tab.account', words: 'donate kaffe coffee' },

	// Notifikationer
	{ path: '/indstillinger/notifikationer', anchor: 'dagens-plan', key: 'ai.plan', tab: 'settings.tab.notifications', words: 'plan morgen daily' },
	{ path: '/indstillinger/notifikationer', anchor: 'push', key: 'push.title', tab: 'settings.tab.notifications', words: 'push besked notification badge' },
	{ path: '/indstillinger/notifikationer', anchor: 'seneste', key: 'push.recent', tab: 'settings.tab.notifications', words: 'log historik' },
	{ path: '/indstillinger/notifikationer', anchor: 'version', key: 'push.version', tab: 'settings.tab.notifications', words: 'opdatering update version', admin: true },

	// Integrationer
	{ path: '/indstillinger/integrationer', anchor: 'gmail', key: 'int.gmail', tab: 'settings.tab.integrations', words: 'gmail google oauth' },
	{ path: '/indstillinger/integrationer', anchor: 'kalender', key: 'int.calendar', tab: 'settings.tab.integrations', words: 'google calendar' },
	{ path: '/indstillinger/integrationer', anchor: 'abonnementer', key: 'int.subscriptions', tab: 'settings.tab.integrations', words: 'ics webcal abonnement subscribe' },
	{ path: '/indstillinger/integrationer', anchor: 'kalenderfeed', key: 'int.feed', tab: 'settings.tab.integrations', words: 'ical ics feed apple thunderbird' },
	{ path: '/indstillinger/integrationer', anchor: 'mail-til-opgave', key: 'int.mailToTask', tab: 'settings.tab.integrations', words: 'mail email forward videresend' },
	{ path: '/indstillinger/integrationer', anchor: 'krog', key: 'int.hookToTask', tab: 'settings.tab.integrations', words: 'webhook hook curl shortcut genvej' },
	{ path: '/indstillinger/integrationer', anchor: 'caldav-server', key: 'int.caldav', tab: 'settings.tab.integrations', words: 'caldav reminders' },
	{ path: '/indstillinger/integrationer', anchor: 'postkasser', key: 'int.mailboxes', tab: 'settings.tab.integrations', words: 'imap icloud fastmail postkasse' },

	// AI
	{ path: '/indstillinger/ai', anchor: 'ai', key: 'ai.title', tab: 'settings.tab.ai', words: 'ai model anthropic openai claude gpt gemini api key' },
	{ path: '/indstillinger/ai', anchor: 'ugentlig', key: 'ai.weekly', tab: 'settings.tab.ai', words: 'uge weekly opsamling' },

	// Tokens
	{ path: '/indstillinger/tokens', anchor: 'tokens', key: 'tokens.title', tab: 'settings.tab.tokens', words: 'token api mcp connector bearer' },

	// Brugere, historik, fejl — kun for administratorer
	{ path: '/indstillinger/brugere', anchor: 'invitation', key: 'users.invite', tab: 'settings.tab.users', words: 'invite invitation bruger user', admin: true },
	{ path: '/indstillinger/brugere', anchor: 'afventende', key: 'users.pending', tab: 'settings.tab.users', words: 'pending afventer', admin: true },
	{ path: '/indstillinger/brugere', anchor: 'konti', key: 'users.accounts', tab: 'settings.tab.users', words: 'konti accounts admin', admin: true },
	{ path: '/indstillinger/historik', anchor: 'historik', key: 'history.title', tab: 'settings.tab.history', words: 'aktivitet activity log', admin: true },
	{ path: '/indstillinger/fejl', anchor: 'fejl', key: 'errors.title', tab: 'settings.tab.errors', words: 'error 500 log', admin: true },

	// Data
	{ path: '/indstillinger/data', anchor: 'import', key: 'data.import', tab: 'settings.tab.data', words: 'import todoist csv json' },
	{ path: '/indstillinger/data', anchor: 'faerdige', key: 'done.title', tab: 'settings.tab.data', words: 'done completed oprydning' },
	{ path: '/indstillinger/data', anchor: 'papirkurv', key: 'data.trash', tab: 'settings.tab.data', words: 'trash slettet deleted' },
	{ path: '/indstillinger/data', anchor: 'sikkerhedskopier', key: 'data.backups', tab: 'settings.tab.data', words: 'backup sikkerhedskopi', admin: true },
	{ path: '/indstillinger/data', anchor: 'eksport', key: 'data.export', tab: 'settings.tab.data', words: 'export eksport zip markdown' },
	{ path: '/indstillinger/data', anchor: 'skabeloner', key: 'data.templates', tab: 'settings.tab.data', words: 'template skabelon' },
	{ path: '/indstillinger/data', anchor: 'beacon', key: 'beacon.title', tab: 'settings.tab.data', words: 'beacon tæller collector telemetri' }
];

/**
 * Samme foldning som serverens `fold`-kolonne.
 *
 * `remove_diacritics` i SQLite gør intet ved ø, æ og å — de er egne bogstaver og
 * ikke accenter — og en dansk søgning, der ikke kan finde "sikkerhedskopier" fra
 * "sikkerhedskopier" skrevet på et fremmed tastatur, er brudt for sine egne
 * brugere. Derfor samme oversættelse her som der, og `normalize` ovenpå til alt
 * det, Unicode faktisk regner for accenter.
 */
export function fold(text) {
	return (text ?? '')
		.toLowerCase()
		.replace(/ø/g, 'o')
		.replace(/æ/g, 'ae')
		.replace(/å/g, 'aa')
		.normalize('NFD')
		.replace(/[\u0300-\u036f]/g, '');
}

/**
 * Indstillingerne, der svarer til det skrevne.
 *
 * Alle ordene skal findes, hvert af dem som begyndelsen af et ord — "sik kop"
 * finder sikkerhedskopierne. Den, der søger, skriver forfra; en søgning, der også
 * matcher midt i et ord, finder "opgave" i "hovedopgaven" og larmer.
 *
 * `t` gives med udefra frem for at blive importeret. Det holder filen fri af
 * Sveltes runes, så den kan læses af en almindelig prøve uden en komponent
 * omkring sig.
 */
export function findSettings(query, { t, isAdmin = false, limit = 8 } = {}) {
	const terms = fold(query).split(/\s+/).filter(Boolean);
	if (!terms.length) return [];

	const hits = [];
	for (const entry of SETTINGS) {
		if (entry.admin && !isAdmin) continue;

		const title = t(entry.key);
		const tab = t(entry.tab);
		const words = fold(`${title} ${tab} ${entry.words}`)
			.split(/[\s/·,-]+/)
			.filter(Boolean);

		// Alle ordene skal findes, og hvert af dem forfra i et ord: "sikker" finder
		// sikkerhedskopierne, "kopier" gør ikke. Den, der søger, skriver
		// begyndelsen af det, den leder efter — en søgning, der også rammer midt i
		// et ord, finder "opgave" inde i "hovedopgaven" og larmer.
		if (!terms.every((term) => words.some((word) => word.startsWith(term)))) continue;

		// Det, der hedder det, man søgte efter, før det, der blot nævner det. Uden
		// den her ligger rækkefølgen fast som filens, og "beacon" ville få hele
		// Data-siden ned over sig, fordi den står først.
		const folded = fold(title);
		const rank = folded.startsWith(terms[0]) ? 0 : folded.includes(terms[0]) ? 1 : 2;
		hits.push({ rank, entry: { ...entry, title, tab, href: `${entry.path}#${entry.anchor}` } });
	}

	return hits
		.sort((a, b) => a.rank - b.rank)
		.slice(0, limit)
		.map((hit) => hit.entry);
}
