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
