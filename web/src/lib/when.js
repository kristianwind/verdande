/**
 * How long ago something was, in the account's language.
 *
 * Four pages had a copy of this — the user list, the error log, the history page
 * and the backup panel — and they had already drifted: one said "aldrig logget
 * ind", another "aldrig", and two of them stopped at days while the others carried
 * on to a date. Four copies is three chances for the fifth one to be different
 * again.
 *
 * Written by hand rather than with `Intl.RelativeTimeFormat`, which would say
 * "3 hours ago" and "for 3 timer siden" correctly and cannot say "lige nu" — and
 * "just now" is the answer for most of what these lists show.
 */
import { t, tag } from './i18n.svelte.js';

export function ago(iso, { never = 'when.never' } = {}) {
	if (!iso) return t(never);

	const then = new Date(iso);
	const seconds = Math.round((Date.now() - then) / 1000);

	if (seconds < 60) return t('when.justNow');
	if (seconds < 3600) return t('when.minutes', { n: Math.floor(seconds / 60) });
	if (seconds < 86400) return t('when.hours', { n: Math.floor(seconds / 3600) });
	if (seconds < 604800) return t('when.days', { n: Math.floor(seconds / 86400) });
	return then.toLocaleDateString(tag(), { day: 'numeric', month: 'short', year: 'numeric' });
}

/** A date and a time, for a list where the time of day matters. */
export function stamp(iso) {
	return new Date(iso).toLocaleString(tag(), {
		day: 'numeric',
		month: 'short',
		hour: '2-digit',
		minute: '2-digit'
	});
}

/** A day, without the year — for something inside the next few months. */
export function shortDate(iso) {
	return new Date(iso).toLocaleDateString(tag(), { day: 'numeric', month: 'short' });
}

/** A day with the year, for something that could be any distance away. */
export function fullDate(iso) {
	return new Date(iso).toLocaleDateString(tag(), {
		day: 'numeric',
		month: 'short',
		year: 'numeric'
	});
}

/**
 * The repeat rules worth a menu entry, and what to call them.
 *
 * Here rather than in the drawer that draws the menu, because the row draws the
 * same rule as a badge and the two had already disagreed: the menu said "Hver
 * dag" in both languages, and the badge said whatever the server said.
 *
 * The server sends `recurrence_text` with every task and sends it in Danish. It
 * has to: one payload goes over the WebSocket to everybody in the project at
 * once, and they do not all read the same language. So the rules that have a name
 * are named here, on the side that knows whose screen this is, and anything more
 * specific — "hver anden tirsdag", typed into the title — falls back to what the
 * server wrote.
 */
export const REPEATS = [
	{ rule: '', label: 'detail.never' },
	{ rule: 'FREQ=DAILY', label: 'detail.daily' },
	{ rule: 'FREQ=WEEKLY', label: 'detail.weekly' },
	{ rule: 'FREQ=WEEKLY;INTERVAL=2', label: 'detail.fortnightly' },
	{ rule: 'FREQ=MONTHLY', label: 'detail.monthly' },
	{ rule: 'FREQ=YEARLY', label: 'detail.yearly' }
];

/** What a repeating task says on its row. */
export function repeatLabel(rule, fallback = '') {
	const known = REPEATS.find((option) => option.rule && option.rule === rule);
	return known ? t(known.label) : fallback;
}
