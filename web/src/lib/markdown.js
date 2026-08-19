/**
 * Just enough Markdown to see what you are writing.
 *
 * Not a renderer: the output is the same text, in the same order, with parts
 * wrapped so they can be styled. That matters because it sits behind a textarea
 * and has to line up with it character for character — a renderer that dropped
 * the `**` or reflowed a list would drift out of alignment on the first bold word.
 *
 * So the marks stay, dimmed. Obsidian hides them except in the line the cursor is
 * in; dimming them always is simpler, does not move text under the cursor as it
 * travels, and still lets you read a document at a glance. It is a smaller idea
 * that survives contact with a textarea.
 */

const INLINE = [
	// Order matters: the longer fence first, or `**bold**` is read as two italics.
	{ kind: 'code', re: /`([^`\n]+)`/g },
	{ kind: 'bold', re: /\*\*([^*\n]+)\*\*/g },
	{ kind: 'italic', re: /\*([^*\n]+)\*/g },
	{ kind: 'tag', re: /(?:^|(?<=\s))#[\p{L}\p{N}_-]{1,64}/gu },
	{ kind: 'wikilink', re: /\[\[[^\]\n]{1,200}\]\]/g }
];

/**
 * segmentsOf splits one line into pieces, each with the kind it should be drawn
 * as. A piece with no kind is ordinary text.
 */
function inlineSegments(line) {
	const marks = [];

	// Each pass reads a copy with the earlier passes' characters blanked out.
	//
	// Skipping overlaps after the fact is not enough, and the failure is subtle:
	// after `**fed**` is claimed as bold, a plain overlap check still lets the
	// italic pass start at the leftover `*` and run to the next one — swallowing
	// " og " and leaving the actual italic word unmarked. Blanking means a later
	// pass cannot see the characters at all.
	let masked = line;
	const blank = (start, end) =>
		(masked = masked.slice(0, start) + '\u0000'.repeat(end - start) + masked.slice(end));

	for (const { kind, re } of INLINE) {
		re.lastIndex = 0;
		let m;
		while ((m = re.exec(masked))) {
			marks.push({ kind, start: m.index, end: m.index + m[0].length });
		}
		for (const mark of marks) blank(mark.start, mark.end);
	}
	marks.sort((a, b) => a.start - b.start);

	const out = [];
	let at = 0;
	for (const mark of marks) {
		if (mark.start > at) out.push({ text: line.slice(at, mark.start) });
		out.push({ text: line.slice(mark.start, mark.end), kind: mark.kind });
		at = mark.end;
	}
	if (at < line.length) out.push({ text: line.slice(at) });
	return out;
}

/**
 * linesOf turns a body into lines, each with a block kind and its inline pieces.
 * Every line is returned, blank ones included, or the mirror would be one line
 * short of the textarea for every empty line in the note.
 */
export function linesOf(body) {
	return body.split('\n').map((line) => {
		const heading = /^(#{1,6})\s/.exec(line);
		if (heading) {
			return { block: `h${heading[1].length}`, parts: inlineSegments(line) };
		}
		if (/^\s*[-*+]\s/.test(line)) return { block: 'list', parts: inlineSegments(line) };
		if (/^\s*\d+\.\s/.test(line)) return { block: 'list', parts: inlineSegments(line) };
		if (/^\s*>\s?/.test(line)) return { block: 'quote', parts: inlineSegments(line) };
		return { block: 'p', parts: inlineSegments(line) };
	});
}
