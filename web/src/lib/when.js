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

/**
 * What a repeating task says on its row.
 *
 * Beskrevet her og ikke på serveren, og det er ikke en smagssag.
 *
 * `recurrence.Describe` skriver dansk. Den kan ikke bare tage en locale med:
 * `toTaskJSON` har niogtyve kaldesteder, og mange af dem er WebSocket-udsendelser,
 * hvor én nyttelast når alle i et projekt på én gang — der findes ikke ét sprog at
 * skrive den i. Reglen ligger allerede i nyttelasten, og den er den samme uanset
 * hvem der læser den. Så beskrivelsen hører hjemme dér, hvor læseren er.
 *
 * Serverens tekst bliver som reserve. Den er stadig den, et API-kald og en
 * MCP-forbindelse får, og de har ingen flade at oversætte i.
 */
export function repeatLabel(rule, fallback = '') {
	if (!rule) return '';
	const known = REPEATS.find((option) => option.rule && option.rule === rule);
	if (known) return t(known.label);
	return describeRule(rule) || fallback;
}

/** Ugedagene i den rækkefølge RRULE navngiver dem. */
const RRULE_DAYS = { MO: 0, TU: 1, WE: 2, TH: 3, FR: 4, SA: 5, SU: 6 };

/**
 * Ugedagens navn på læserens sprog, fra Intl frem for fra en tabel.
 *
 * Ikke for at spare syv strenge pr. sprog — for at få de navne, sproget faktisk
 * bruger. Den 1. januar 2024 var en mandag; enhver mandag ville gøre det, og en
 * fast en holder det ude af tegningen.
 */
function dayName(code) {
	const index = RRULE_DAYS[code];
	if (index === undefined) return code;
	return new Intl.DateTimeFormat(tag(), { weekday: 'long' }).format(
		new Date(Date.UTC(2024, 0, 1 + index))
	);
}

/**
 * En RRULE sagt med ord.
 *
 * Dækker det, verdandes egen hurtig tilføjelse kan lave, og falder tilbage til
 * reglen selv for alt andet — en kalenderklient kan sende ting, ingen her har
 * skrevet, og en halv oversættelse af dem ville være værre end den rå regel.
 */
export function describeRule(rule) {
	if (!rule) return '';
	const parts = {};
	for (const pair of rule.replace(/^RRULE:/i, '').split(';')) {
		const [key, value] = pair.split('=');
		if (key && value !== undefined) parts[key.toUpperCase()] = value;
	}

	const interval = Number(parts.INTERVAL) > 0 ? Number(parts.INTERVAL) : 1;

	if (parts.BYDAY) {
		if (parts.BYDAY === 'MO,TU,WE,TH,FR') return t('repeat.weekdays');
		if (parts.BYDAY === 'SA,SU') return t('repeat.weekends');

		// Sorteret som ugen går, ikke som reglen tilfældigvis skrev dem: "mandag og
		// torsdag" og "torsdag og mandag" er den samme regel, og den ene af dem er
		// den, folk siger.
		const names = parts.BYDAY.split(',')
			.filter((code) => code in RRULE_DAYS)
			.sort((a, b) => RRULE_DAYS[a] - RRULE_DAYS[b])
			.map(dayName);
		if (!names.length) return '';

		const joined =
			names.length === 1
				? names[0]
				: names.slice(0, -1).join(', ') + ' ' + t('repeat.and') + ' ' + names.at(-1);

		// "hver mandag", ikke "hver uge mandag". En ugentlig regel, der navngiver
		// sine dage, har ikke også brug for ordet for intervallet.
		if (interval <= 1) return t('repeat.everyDay', { day: joined });
		// To har sit eget ord på begge sprog — "hver anden", "every other" — og det
		// er ikke tallet to sat ind i den samme sætning. Et sprog, der bøjer, kan
		// ikke sættes sammen af stumper.
		if (interval === 2) return t('repeat.everyOtherWeekOn', { day: joined });
		return t('repeat.everyNthWeekOn', { n: interval, day: joined });
	}

	switch (parts.FREQ?.toUpperCase()) {
		case 'DAILY':
			return every(interval, 'day');
		case 'WEEKLY':
			return every(interval, 'week');
		case 'MONTHLY':
			if (parts.BYMONTHDAY) return t('repeat.monthlyOn', { day: parts.BYMONTHDAY });
			return every(interval, 'month');
		case 'YEARLY':
			return every(interval, 'year');
		default:
			return '';
	}
}

/**
 * "hver dag", "hver anden uge", "hver tredje måned".
 *
 * Tallet står i teksten frem for at blive sat sammen af stumper: "every other
 * week" og "hver anden uge" er ikke to ord byttet om, og et sprog, der bøjer
 * enheden efter tallet, kan ikke sættes sammen af to nøgler.
 */
function every(interval, unit) {
	if (interval <= 1) return t(`repeat.every.${unit}`);
	if (interval === 2) return t(`repeat.everyOther.${unit}`);
	return t(`repeat.everyN.${unit}`, { n: interval });
}
