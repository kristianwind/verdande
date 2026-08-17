/**
 * The colours a project, a group, a label or a filter can be marked with.
 *
 * The `color` column has been on projects since the first migration, defaulting
 * to 'graphite', and nothing has ever read it. This is the list that makes it
 * mean something.
 *
 * **One set for all five themes, deliberately.** Every other colour in this app is
 * redefined per theme, because every other colour is text, a ground, or a border,
 * and those have to be measured against the surface behind them. These are none of
 * those: they are small solid marks sitting next to a label that carries the
 * meaning. What they need is to be told apart from each other, and to be visible
 * on both a near-black and a white ground — which mid-tone, medium-saturation
 * colours are, and which is why they are all in that band.
 *
 * Defining them once is also what keeps them out of the trap the themes are in:
 * a colour added to four theme blocks and forgotten in the fifth looks finished
 * and falls back silently. There is no fifth block to forget.
 *
 * The ids are what the database stores, so they are not to be renamed. `app.css`
 * carries a `--color-<id>` token for each, and a Go test reads this file to check
 * the server's list of accepted names has not drifted from it.
 */
export const COLORS = [
	{ id: 'graphite', name: 'Grafit' },
	{ id: 'tomato', name: 'Tomat' },
	{ id: 'amber', name: 'Rav' },
	{ id: 'lime', name: 'Lime' },
	{ id: 'green', name: 'Grøn' },
	{ id: 'teal', name: 'Petrol' },
	{ id: 'blue', name: 'Blå' },
	{ id: 'indigo', name: 'Indigo' },
	{ id: 'violet', name: 'Violet' },
	{ id: 'magenta', name: 'Magenta' }
];

/** The CSS variable a colour id paints with, falling back to the default. */
export function colorVar(id) {
	return COLORS.some((c) => c.id === id) ? `var(--color-${id})` : 'var(--color-graphite)';
}

/** The Danish name, for the swatch's label. */
export function colorName(id) {
	return COLORS.find((c) => c.id === id)?.name ?? COLORS[0].name;
}
