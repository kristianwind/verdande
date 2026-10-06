/**
 * En indsat liste, læst som opgaver og underopgaver.
 *
 * Quick-add er ét felt på én linje, og en browser smider linjeskiftene væk, når
 * man indsætter i et `<input>` — så en kopieret liste blev til én opgave med det
 * hele mast sammen. Det her er den anden halvdel: hvad linjerne betyder.
 *
 * Indrykning bærer niveauet, samme regel som punktlisterne i noterne. Bredden
 * læses af teksten selv frem for at være gættet til to eller fire: en liste fra
 * Apple Noter rykker ind med ét antal mellemrum, en fra et Markdown-dokument med
 * et andet, og begge dele skal give ét niveau. En stak over de indrykninger, der
 * faktisk er set, klarer begge uden at kende tallet på forhånd.
 *
 * Mærkerne — `-`, `*`, `+`, `1.`, `1)` — fjernes, fordi de er listens notation og
 * ikke en del af opgaven. En afkrydsningsboks er derimod indhold: `- [x]` siger,
 * at punktet var gjort, og en halvt afkrydset liste, der kommer ind som lutter
 * uløste opgaver, har tabt det, man indsatte den for.
 *
 * Teksten selv røres ikke ud over det. Datoer, `#projekt` og `@etiket` læses af
 * quickadd på serveren, præcis som når linjen var skrevet i hånden — ét sted at
 * forstå en opgavelinje, ikke to.
 */

/** `- `, `* `, `+ `, `1. `, `1) ` — listens egen notation, ikke opgavens navn. */
const MARKER = /^(?:[-*+]|\d+[.)])\s+/;
/** `[ ]`, `[x]`, `[X]` — står efter mærket, når der er et. */
const BOX = /^\[([ xX])\]\s*/;

/** Hvor bredt et stykke indrykning er, med en tabulator som to mellemrum. */
function width(indent) {
	return indent.replace(/\t/g, '  ').length;
}

export function readList(text) {
	const out = [];
	// De indrykningsbredder, der står åbne lige nu — én pr. niveau.
	const levels = [];

	for (const raw of (text ?? '').split(/\r?\n/)) {
		const indent = /^[ \t]*/.exec(raw)[0];
		let rest = raw.slice(indent.length).trim();
		// En tom linje er et mellemrum i den, man kopierede, ikke et punkt. Den
		// nulstiller ikke niveauet: en liste med luft mellem punkterne er én liste.
		if (!rest) continue;

		rest = rest.replace(MARKER, '');
		let done = false;
		const box = BOX.exec(rest);
		if (box) {
			done = box[1] !== ' ';
			rest = rest.slice(box[0].length);
		}
		rest = rest.trim();
		if (!rest) continue;

		const w = width(indent);
		while (levels.length && w < levels[levels.length - 1]) levels.pop();
		if (!levels.length || w > levels[levels.length - 1]) levels.push(w);

		out.push({ depth: levels.length - 1, text: rest, done });
	}

	return out;
}
