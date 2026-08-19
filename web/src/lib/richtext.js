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

/** Inline marks, innermost first so a bold word inside italics survives both. */
function inlineToHtml(text) {
	return escapeHtml(text)
		.replace(/&lt;u&gt;(.+?)&lt;\/u&gt;/g, '<u>$1</u>')
		.replace(/`([^`]+)`/g, '<code>$1</code>')
		.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
		.replace(/~~(.+?)~~/g, '<s>$1</s>')
		.replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>');
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
	let list = null;

	const closeList = () => {
		if (list) {
			out.push(`</${list}>`);
			list = null;
		}
	};

	for (const line of lines) {
		const bullet = /^[-*+]\s+(.*)$/.exec(line);
		const numbered = /^\d+\.\s+(.*)$/.exec(line);

		if (bullet || numbered) {
			const want = bullet ? 'ul' : 'ol';
			if (list !== want) {
				closeList();
				out.push(`<${want}>`);
				list = want;
			}
			out.push(`<li>${inlineToHtml((bullet ?? numbered)[1])}</li>`);
			continue;
		}
		closeList();

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
	return out.join('');
}

/** The inline marks, read back off the DOM. */
function inlineToMarkdown(node) {
	if (node.nodeType === Node.TEXT_NODE) return node.textContent;
	if (node.nodeType !== Node.ELEMENT_NODE) return '';

	const inner = [...node.childNodes].map(inlineToMarkdown).join('');
	switch (node.tagName) {
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
export function htmlToMarkdown(root) {
	const lines = [];

	const walk = (node) => {
		for (const child of node.childNodes) {
			if (child.nodeType === Node.TEXT_NODE) {
				if (child.textContent.trim()) lines.push(child.textContent);
				continue;
			}
			if (child.nodeType !== Node.ELEMENT_NODE) continue;

			switch (child.tagName) {
				case 'UL':
				case 'OL': {
					let n = 1;
					for (const li of child.children) {
						const text = inlineToMarkdown(li).trim();
						lines.push(child.tagName === 'UL' ? `- ${text}` : `${n++}. ${text}`);
					}
					break;
				}
				case 'DIV':
					// The browser's own container when nothing else applies. Read through
					// it rather than treating it as a block, or every paste adds a level.
					if ([...child.children].some((c) => /^(P|H[1-3]|UL|OL|PRE|BLOCKQUOTE|DIV)$/.test(c.tagName))) {
						walk(child);
					} else {
						lines.push(inlineToMarkdown(child));
					}
					break;
				default: {
					const block = BLOCKS.find((b) => b.tag === child.tagName.toLowerCase());
					const text = inlineToMarkdown(child);
					lines.push(block ? block.prefix + text : text);
				}
			}
		}
	};

	walk(root);
	// Trailing blank lines are what an editor accumulates from people pressing
	// return at the end; they are not content and they grow every time.
	while (lines.length && lines[lines.length - 1].trim() === '') lines.pop();
	return lines.join('\n');
}
