/**
 * Splits a plain string into runs of text and the URLs inside it, so a task's
 * title or description can be shown with the links clickable without the text
 * ever having been anything but plain text on disk.
 *
 * A task is not rich text — the title is an `<input>` and the description a
 * `<textarea>` — so there is nowhere to *store* a link. What there is instead is
 * a person who pasted one in and expects to be able to click it. This finds the
 * ones that are unambiguously links and leaves everything else exactly as typed.
 *
 * Deliberately conservative about what counts as a link: an explicit
 * `http://`/`https://`, or a bare `www.` host. "example.com" on its own is not
 * matched — most of those are sentences ("the file.txt is here"), and a false
 * link in a title is worse than a real one left plain.
 */

// A trailing `.,;:!?` or a closing bracket is almost always punctuation the
// sentence owns rather than part of the URL, so the run stops before it. A `)`
// is only dropped when the URL holds no `(` of its own — Wikipedia-style links
// carry balanced parentheses that belong to the address.
const URL_RE = /\b(?:https?:\/\/|www\.)[^\s<>]+/gi;

function trimTrailing(url) {
	let end = url.length;
	while (end > 0) {
		const ch = url[end - 1];
		if ('.,;:!?"\''.includes(ch)) {
			end--;
			continue;
		}
		if (ch === ')' && !url.slice(0, end).includes('(')) {
			end--;
			continue;
		}
		break;
	}
	return url.slice(0, end);
}

/**
 * @param {string} text
 * @returns {Array<{ text: string, href?: string }>} runs in order; a run with an
 *   `href` is a link, one without is plain text. An empty or link-free string
 *   comes back as a single text run (possibly empty) so callers can render it the
 *   same way regardless.
 */
export function linkify(text) {
	const value = text ?? '';
	const runs = [];
	let last = 0;

	for (const match of value.matchAll(URL_RE)) {
		const raw = trimTrailing(match[0]);
		if (!raw) continue;
		const start = match.index;

		if (start > last) runs.push({ text: value.slice(last, start) });

		const href = raw.startsWith('www.') ? `https://${raw}` : raw;
		runs.push({ text: raw, href });
		last = start + raw.length;
	}

	if (last < value.length) runs.push({ text: value.slice(last) });
	if (!runs.length) runs.push({ text: value });

	return runs;
}
