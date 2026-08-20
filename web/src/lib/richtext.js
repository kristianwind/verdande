/**
 * Between what you see and what is stored.
 *
 * The editor is rich text; the note on disk is Markdown. That is not a compromise
 * anybody has to live with silently — it is the reason the export is trustworthy:
 * the file a note becomes is the file it already was. So these two functions are
 * the seam, and they have to agree with each other exactly, or a note changes
 * shape a little every time it is opened and saved.
 *
 * The set of styles is deliberately small and matches what Apple Notes offers,
 * because that is the vocabulary somebody coming from there already has:
 *
 *     Titel        → # 
 *     Overskrift   → ## 
 *     Underrubrik  → ### 
 *     Brødtekst    → nothing
 *     Monotype     → four spaces
 *     Punktliste   → - 
 *     Nummereret   → 1. 
 *     Blokcitat    → > 
 *
 * Bold, italic and strikethrough have Markdown of their own. Underline does not —
 * there is no such thing in any Markdown dialect — so it is stored as inline HTML,
 * which every Markdown reader passes through untouched. It is the one place where
 * the file is not pure Markdown, and it is that rather than dropping a style the
 * source program has.
 */

const BLOCKS = [
	{ style: 'title', tag: 'h1', prefix: '# ' },
	{ style: 'heading', tag: 'h2', prefix: '## ' },
	{ style: 'subheading', tag: 'h3', prefix: '### ' },
	{ style: 'quote', tag: 'blockquote', prefix: '> ' },
	{ style: 'mono', tag: 'pre', prefix: '    ' }
];

const escapeHtml = (s) =>
	s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

/**
 * En værdi, der skal stå inde i en attribut.
 *
 * escapeHtml er skrevet til tekst mellem to tags, hvor et anførselstegn ikke
 * betyder noget, og lader dem derfor stå. Inde i en attribut lukker de den — og
 * så står resten af strengen som markup. Det er den ene forskel, og den er hele
 * forskellen mellem tekst og et onerror.
 */
const attr = (s) => escapeHtml(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');

/**
 * Billeder løftes ud af teksten, før noget andet rører den.
 *
 * De stod før først i kæden, og så kørte de fire resterende regler hen over en
 * streng, der nu indeholdt rigtig HTML — ind i den `alt`, der lige var lavet.
 * Det gik godt, så længe ingen af erstatningerne indeholdt et anførselstegn, og
 * det er en betingelse, der stod skrevet ingen steder: den dag nogen tilføjer en
 * regel for links, er den brudt, og det er præcis den vej, XSS-en kom ind ad.
 *
 * Værre endnu var undslupningen. `escapeHtml` kørte på hele teksten, og `attr`
 * kørte den igen på `alt` — så et og-tegn blev til en dobbelt undslupning, der
 * voksede ved hver eneste åbning og gemning. En note med "Kaffe & te" i et
 * billede blev længere, hver gang nogen så på den.
 *
 * En pladsholder løser begge dele. Billedet tages ud af den rå tekst, resten
 * undslippes og formateres som altid, og billedet lægges tilbage til sidst med en
 * `alt`, der er undsluppet nøjagtig én gang. Rækkefølgen af de øvrige regler kan
 * så ikke længere betyde noget.
 *
 * Nul-tegnet som mærke, fordi det er det ene tegn, en note ikke kan indeholde:
 * det overlever hverken en contenteditable eller en Markdown-fil.
 */
const IMAGE = /!\[([^\]]*)\]\((\/api\/v1\/attachments\/[0-9a-f-]+)\)/g;
const MARK = "\u0000";

/** Inline marks, innermost first so a bold word inside italics survives both. */
function inlineToHtml(text) {
	// Kun vores egen adresse bliver til et billede. Et `![](http://…)` fra en
	// indsat tekst ville ellers hente fra en fremmed vært, når noten åbnes, og
	// fortælle den, at den er blevet læst — og en note er det sidste sted, man vil
	// have en sporingspixel. Alt andet står som den tekst, det er.
	const images = [];
	const lifted = (text ?? '').replace(IMAGE, (_, alt, src) => {
		images.push(`<img src="${src}" alt="${attr(alt)}">`);
		return `${MARK}${images.length - 1}${MARK}`;
	});

	const marked = escapeHtml(lifted)
		.replace(/&lt;u&gt;(.+?)&lt;\/u&gt;/g, '<u>$1</u>')
		.replace(/`([^`]+)`/g, '<code>$1</code>')
		.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
		.replace(/~~(.+?)~~/g, '<s>$1</s>')
		.replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>');

	return marked.replace(new RegExp(MARK + '(\\d+)' + MARK, 'g'), (_, i) => images[Number(i)] ?? '');
}

/**
 * markdownToHtml renders a note for the editor.
 *
 * Every line becomes exactly one block. Markdown would normally join consecutive
 * lines into one paragraph; here it must not, because the person typed a line and
 * expects to get it back — an editor that silently reflows what somebody wrote is
 * an editor they stop trusting.
 */
export function markdownToHtml(markdown) {
	const lines = (markdown ?? '').split('\n');
	const out = [];
	let fence = null; // sproget, mens vi er inde i en ```-blok

	// Listeniveauerne, der står åbne lige nu — ét mærke pr. dybde.
	//
	// En stak frem for én `list`-variabel, fordi en punktliste kan ligge inde i en
	// anden. Den flade udgave kunne kun beskrive ét niveau, så et indrykket punkt
	// blev lagt ved siden af sit ophav og kom tilbage som søskende: indrykningen
	// var væk efter én gemning.
	//
	// Underlisten lægges *inde i* sit `<li>`, ikke ved siden af. Browsere viser
	// begge dele ens, men kun den ene er en liste i en liste, når den læses tilbage
	// — og det er læsningen tilbage, der skriver filen.
	const stack = [];
	let liOpen = false;

	const closeItem = () => {
		if (liOpen) {
			out.push('</li>');
			liOpen = false;
		}
	};

	const closeList = (toDepth = 0) => {
		while (stack.length > toDepth) {
			closeItem();
			out.push(`</${stack.pop()}>`);
			// Efter et niveau lukkes, står vi igen inde i ophavets `<li>`.
			liOpen = stack.length > 0;
		}
		if (stack.length === 0) liOpen = false;
	};

	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		// Hegnede blokke først: alt indeni er tekst, ikke Markdown, og en linje der
		// begynder med - inde i et shell-script er ikke et punkt i en liste.
		const fenced = /^```(\w*)\s*$/.exec(line);
		if (fence !== null) {
			if (fenced) {
				out.push('</code></pre>');
				fence = null;
			} else {
				out.push(escapeHtml(line) + '\n');
			}
			continue;
		}
		if (fenced) {
			closeList();
			fence = fenced[1] ?? '';
			// attr, ikke escapeHtml: værdien står inde i en attribut. Den er sikker i
			// dag alene fordi hegn-regexen er \w*, som ikke rummer anførselstegn — og
			// den dag nogen udvider den for at tage imod "c++" eller "f#", er det en
			// XSS. Undslupningen skal passe til stedet, ikke til det, der
			// tilfældigvis kan stå der nu.
			out.push(`<pre data-lang="${attr(fence)}"><code>`);
			continue;
		}

		// Indrykning bærer niveauet: to mellemrum, eller en tabulator, er ét trin.
		const item = /^([ \t]*)(?:([-*+])|(\d+)\.)\s+(.*)$/.exec(line);

		if (item) {
			const indent = item[1].replace(/\t/g, '  ').length;
			const depth = Math.floor(indent / 2);
			const want = item[2] ? 'ul' : 'ol';

			// Dybere end vi står: luk ned til niveauet. Én ad gangen, så hvert
			// `</ul>` lander inde i det `<li>`, det hører til.
			closeList(depth + 1);

			// Samme dybde, men den anden slags liste — en punktliste efterfulgt af en
			// nummereret er to lister, ikke én med blandede mærker.
			if (stack.length === depth + 1 && stack[depth] !== want) {
				closeList(depth);
			}

			while (stack.length < depth + 1) {
				// Nummeret tælles ikke om. Skrev nogen 10., 11., er det dét, der står i
				// filen, og en liste, der begynder ved ti, skal begynde ved ti — ellers
				// gør en gemning uden ændringer noten om.
				const start = item[3] && item[3] !== '1' && stack.length === depth ? ` start="${Number(item[3])}"` : '';
				out.push(`<${want}${want === 'ol' ? start : ''}>`);
				stack.push(want);
				liOpen = false;
			}

			closeItem();
			out.push(`<li>${inlineToHtml(item[4])}`);
			liOpen = true;
			continue;
		}
		closeList();

		// Et blokcitat, der rummer mere end én linje, læses som ét citat med
		// indholdet indeni — samme vej tilbage som htmlToMarkdown skrev det. Uden
		// det blev en liste i et citat til seks citater med et bindestreg i hver.
		if (/^>(?: |$)/.test(line)) {
			const inner = [];
			while (i < lines.length && /^>(?: |$)/.test(lines[i])) {
				inner.push(lines[i].replace(/^> ?/, ''));
				i++;
			}
			i--;
			out.push(`<blockquote>${markdownToHtml(inner.join('\n'))}</blockquote>`);
			continue;
		}

		const block = BLOCKS.find((b) =>
			b.prefix === '    ' ? line.startsWith('    ') : line.startsWith(b.prefix)
		);
		if (block) {
			out.push(`<${block.tag}>${inlineToHtml(line.slice(block.prefix.length))}</${block.tag}>`);
			continue;
		}

		// An empty line is still a line. Without the break the browser collapses the
		// block to nothing and the caret has nowhere to stand.
		out.push(`<p>${line.trim() === '' ? '<br>' : inlineToHtml(line)}</p>`);
	}
	closeList();
	if (fence !== null) out.push('</code></pre>');
	return out.join('');
}

/** The inline marks, read back off the DOM. */
function inlineToMarkdown(node) {
	if (node.nodeType === Node.TEXT_NODE) return node.textContent;
	if (node.nodeType !== Node.ELEMENT_NODE) return '';

	const inner = [...node.childNodes].map(inlineToMarkdown).join('');
	switch (node.tagName) {
		// Et billede har ingen tekst i sig, så uden det her læses det som ingenting
		// — og den første gemning ville tage hvert billede ud af noten uden at
		// nogen havde rørt det.
		case 'IMG':
			return `![${node.getAttribute('alt') ?? ''}](${node.getAttribute('src') ?? ''})`;
		case 'STRONG':
		case 'B':
			return inner ? `**${inner}**` : '';
		case 'EM':
		case 'I':
			return inner ? `*${inner}*` : '';
		case 'S':
		case 'STRIKE':
		case 'DEL':
			return inner ? `~~${inner}~~` : '';
		case 'U':
			return inner ? `<u>${inner}</u>` : '';
		case 'CODE':
			return inner ? `\`${inner}\`` : '';
		case 'SPAN': {
			// The browser's own way of saying bold or italic when it is writing styles
			// rather than tags. Read as well as tags, because `styleWithCSS` is not
			// honoured everywhere and a note must not lose a format on the way out.
			const style = node.getAttribute('style') ?? '';
			if (/font-weight:\s*(bold|[6-9]00)/.test(style)) return inner ? `**${inner}**` : '';
			if (/font-style:\s*italic/.test(style)) return inner ? `*${inner}*` : '';
			if (/text-decoration[^;]*line-through/.test(style)) return inner ? `~~${inner}~~` : '';
			if (/text-decoration[^;]*underline/.test(style)) return inner ? `<u>${inner}</u>` : '';
			return inner;
		}
		case 'BR':
			return '';
		default:
			return inner;
	}
}

/**
 * htmlToMarkdown reads the editor back.
 *
 * Written against the DOM rather than against a string of HTML, because that is
 * what contenteditable actually produces — and what it produces is not tidy. The
 * browser will nest, split and re-wrap as somebody types, and a regular expression
 * over that is a guess.
 */
/** The tags that are a block of their own, and must never be read as inline text. */
const BLOCK_TAGS = /^(P|H[1-6]|UL|OL|LI|PRE|BLOCKQUOTE|DIV)$/;

/** Whether an element holds blocks, in which case reading it as one line loses them. */
const holdsBlocks = (el) => [...el.children].some((c) => BLOCK_TAGS.test(c.tagName));

export function htmlToMarkdown(root) {
	const lines = [];

	/**
	 * @param node  hvad der læses
	 * @param emit  hvor linjerne lægges. Et blokcitat giver sin egen, som sætter
	 *              `> ` foran alt, hvad der kommer indefra — sådan kan et citat
	 *              indeholde afsnit og lister uden at de bliver til én linje.
	 */
	const walk = (node, emit) => {
		for (const child of node.childNodes) {
			if (child.nodeType === Node.TEXT_NODE) {
				if (child.textContent.trim()) emit(child.textContent);
				continue;
			}
			if (child.nodeType !== Node.ELEMENT_NODE) continue;

			switch (child.tagName) {
				case 'UL':
				case 'OL': {
					list(child, 0, emit);
					break;
				}

				// Et citat kan rumme blokke. Læst som én streng blev "En", "To" til
				// "EnTo", og en liste inde i et citat mistede hvert eneste punktmærke
				// — det var sådan en note kom tilbage som én lang linje efter en
				// gemning, uden at nogen havde rørt teksten.
				case 'BLOCKQUOTE': {
					if (holdsBlocks(child)) {
						walk(child, (l) => emit(l.trim() === '' ? '>' : `> ${l}`));
					} else {
						emit('> ' + inlineToMarkdown(child));
					}
					break;
				}
				case 'DIV':
					// The browser's own container when nothing else applies. Read through
					// it rather than treating it as a block, or every paste adds a level.
					if (holdsBlocks(child)) {
						walk(child, emit);
					} else {
						emit(inlineToMarkdown(child));
					}
					break;
				case 'PRE': {
					// Back out as a fence, with the language it came in with. The old
					// four-space form is still read on the way in; it is not written
					// out any more, because a fence survives being pasted somewhere
					// else and an indent does not.
					const lang = child.getAttribute('data-lang') ?? '';
					emit('```' + lang);
					// Knapper og andet, editoren har lagt oven på blokken, hører ikke til
					// i filen. Kopiér-knappen ligger inde i <pre> for at kunne placeres i
					// hjørnet af den, og `textContent` ville ellers skrive ordet "Kopiér"
					// ind i hver eneste kodeblok, hver gang noten blev gemt.
					const code = [...child.childNodes]
						.filter((n) => n.nodeType !== Node.ELEMENT_NODE || !n.classList?.contains('copy'))
						.map((n) => n.textContent ?? '')
						.join('');
					for (const l of code.replace(/\n$/, '').split('\n')) {
						emit(l);
					}
					emit('```');
					break;
				}
				default: {
					const block = BLOCKS.find((b) => b.tag === child.tagName.toLowerCase());
					// En overskrift med en liste i sig er stadig en liste. Samme fælde som
					// citatet: læst som én linje forsvandt hvert punktmærke.
					if (holdsBlocks(child)) {
						walk(child, emit);
						break;
					}
					const text = inlineToMarkdown(child);
					emit(block ? block.prefix + text : text);
				}
			}
		}
	};

	/**
	 * Én liste, og de lister der ligger i den.
	 *
	 * `start` læses med, så en liste, der begynder ved ti, stadig gør det. Talt om
	 * fra ét gjorde en gemning uden ændringer noten om — den slags er værre end en
	 * fejl, fordi den sker uden at nogen har rørt noget.
	 */
	const list = (el, depth, emit) => {
		let n = Number(el.getAttribute('start') || 1);
		if (!Number.isFinite(n) || n < 1) n = 1;
		const pad = '  '.repeat(depth);

		for (const li of el.children) {
			if (li.tagName !== 'LI') continue;

			// Punktets egen tekst er alt undtagen de lister, der ligger under det.
			const nested = [...li.children].filter((c) => c.tagName === 'UL' || c.tagName === 'OL');
			let text;
			if (nested.length) {
				const clone = li.cloneNode(true);
				for (const sub of [...clone.children]) {
					if (sub.tagName === 'UL' || sub.tagName === 'OL') sub.remove();
				}
				text = inlineToMarkdown(clone).trim();
			} else {
				text = inlineToMarkdown(li).trim();
			}

			emit(el.tagName === 'UL' ? `${pad}- ${text}` : `${pad}${n++}. ${text}`);
			for (const sub of nested) list(sub, depth + 1, emit);
		}
	};

	walk(root, (l) => lines.push(l));
	// Trailing blank lines are what an editor accumulates from people pressing
	// return at the end; they are not content and they grow every time.
	while (lines.length && lines[lines.length - 1].trim() === '') lines.pop();
	return lines.join('\n');
}
