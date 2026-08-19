/**
 * Farver til en kodeblok.
 *
 * Ikke et bibliotek: et fuldt syntaksbibliotek er hundredvis af kilobytes for at
 * gøre nogle ord blå i en note. Det her er et lille sæt regler pr. sprog, der
 * rammer det, øjet leder efter — strenge, tal, kommentarer, nøgleord — og som er
 * ligeglad med resten. En farve for lidt er ingenting; en forkert er heller ikke
 * meget, når teksten stadig står der, som den blev skrevet.
 *
 * Reglerne prøves i rækkefølge, og den første, der rammer, vinder. Strenge og
 * kommentarer står derfor først: et nøgleord inde i en streng er ikke et nøgleord.
 */

const COMMON = {
	string: /^("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`(?:[^`\\]|\\.)*`)/,
	number: /^\b\d+(\.\d+)?\b/
};

const LANGUAGES = {
	yaml: [
		['comment', /^#[^\n]*/],
		['string', COMMON.string],
		// Nøglen, som er det, man scanner efter i en YAML-fil.
		['key', /^[ \t]*[-\w.]+(?=\s*:)/],
		['punct', /^[:\-|>]/],
		['number', COMMON.number],
		['bool', /^\b(true|false|null|yes|no|on|off)\b/i]
	],
	json: [
		['string', COMMON.string],
		['punct', /^[{}[\],:]/],
		['number', COMMON.number],
		['bool', /^\b(true|false|null)\b/]
	],
	bash: [
		['comment', /^#[^\n]*/],
		['string', COMMON.string],
		// Prompten er ikke kode, men den er det første øjet finder i en udskrift.
		['prompt', /^\S+@\S+:[^$#]*[$#]/],
		['variable', /^\$\{?\w+\}?/],
		['keyword', /^\b(if|then|else|fi|for|in|do|done|while|case|esac|function|return|export|local|sudo)\b/],
		['flag', /^\s-{1,2}[\w-]+/],
		['number', COMMON.number]
	],
	go: [
		['comment', /^\/\/[^\n]*/],
		['string', COMMON.string],
		['keyword', /^\b(func|package|import|var|const|type|struct|interface|if|else|for|range|return|defer|go|chan|select|switch|case|default|map|nil|true|false)\b/],
		['number', COMMON.number]
	],
	js: [
		['comment', /^\/\/[^\n]*/],
		['string', COMMON.string],
		['keyword', /^\b(const|let|var|function|return|if|else|for|while|import|export|from|class|new|await|async|try|catch|throw|typeof|null|undefined|true|false)\b/],
		['number', COMMON.number]
	],
	sql: [
		['comment', /^--[^\n]*/],
		['string', COMMON.string],
		['keyword', /^\b(select|from|where|insert|into|values|update|set|delete|create|table|index|join|left|inner|on|and|or|not|null|primary|key|references|order|by|group|limit)\b/i],
		['number', COMMON.number]
	]
};

// Det, folk skriver, mod det, filen hedder.
const ALIASES = {
	sh: 'bash', shell: 'bash', zsh: 'bash', console: 'bash', terminal: 'bash',
	yml: 'yaml', javascript: 'js', ts: 'js', typescript: 'js', golang: 'go'
};

const escape = (s) =>
	s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

/**
 * Gætter sproget, når blokken ikke siger det.
 *
 * Med vilje forsigtigt: at gætte forkert farver de forkerte ord, og en
 * terminaludskrift uden farver er stadig læselig. Kun mønstre, der er svære at
 * forveksle, tæller med.
 */
export function guessLanguage(code) {
	if (/^\s*[{[]/.test(code) && /["}\]]\s*$/.test(code.trim())) return 'json';
	if (/^\s*\S+@\S+:.*[$#]/m.test(code)) return 'bash';
	if (/^\s*(package|func)\s+\w/m.test(code)) return 'go';
	if (/^\s*[-\w.]+:\s*(\S|$)/m.test(code) && !/[;{}]/.test(code)) return 'yaml';
	if (/^\s*(SELECT|INSERT|UPDATE|CREATE TABLE)\b/im.test(code)) return 'sql';
	return '';
}

/** Kode til HTML med spans. Ukendt sprog giver teksten uændret, blot undsluppet. */
export function highlight(code, language) {
	const rules = LANGUAGES[ALIASES[language] ?? language];
	if (!rules) return escape(code);

	let out = '';
	let rest = code;
	let guard = 0;

	while (rest && guard++ < 20000) {
		let matched = false;
		for (const [kind, re] of rules) {
			const m = re.exec(rest);
			if (!m || !m[0]) continue;
			out += `<span class="tok-${kind}">${escape(m[0])}</span>`;
			rest = rest.slice(m[0].length);
			matched = true;
			break;
		}
		if (!matched) {
			// Ét tegn frem. Langsommere end at springe til næste interessante sted og
			// umuligt at få til at gå i stå, hvilket er den rigtige byttehandel i noget,
			// der kører på hvert tastetryk i en note, nogen sidder og skriver i.
			out += escape(rest[0]);
			rest = rest.slice(1);
		}
	}
	return out + escape(rest);
}
