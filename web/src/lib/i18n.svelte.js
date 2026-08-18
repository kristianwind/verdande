/**
 * The interface's language.
 *
 * Every string a person reads used to be written into the component that drew it,
 * in Danish. The account already had a `locale` — but it only chose which grammar
 * quick add parsed a line with, so setting it to English changed nothing anybody
 * could see. This is the other half of that field.
 *
 * Danish is the fallback and the source of truth. It is the language the app was
 * written in, and it is the one whose phrasing was argued over; English is a
 * translation of it. A key missing from English therefore falls through to Danish
 * rather than to the key, because a Danish sentence in an English screen is
 * legible and `settings.notifications.title` is not.
 *
 * `t()` reads `i18n.locale`, which is `$state`, so every component that calls it
 * re-renders when the account's language changes. That is what makes the setting
 * take effect without a reload — and it is why this file is `.svelte.js`.
 */
import { da } from './locales/da.js';
import { en } from './locales/en.js';

const DICTIONARIES = { da, en };

class I18n {
	/**
	 * Starts Danish and is set from the account as soon as the session loads.
	 *
	 * Not read from localStorage on the way in. The language belongs to the
	 * account — the same person reading in Danish on their laptop wants Danish on
	 * their phone — unlike the sidebar's width, which belongs to the screen.
	 */
	locale = $state('da');

	set(locale) {
		this.locale = locale in DICTIONARIES ? locale : 'da';
	}
}

export const i18n = new I18n();

/**
 * The string for `key`, with `{name}` placeholders filled from `params`.
 *
 * Placeholders rather than string concatenation, because word order is not a
 * constant across languages: "3 tasks left" and "3 opgaver tilbage" happen to
 * agree, and "shared with me" and "delt med mig" do not.
 */
export function t(key, params) {
	const dict = DICTIONARIES[i18n.locale] ?? da;
	const raw = dict[key] ?? da[key] ?? key;
	if (!params) return raw;
	return raw.replace(/\{(\w+)\}/g, (whole, name) => (name in params ? String(params[name]) : whole));
}

/**
 * The plural form for `n`.
 *
 * Both languages here have exactly two forms, so this takes the two rather than
 * pulling in a plural-rules engine for a case neither language has. The count is
 * substituted as `{n}`, so "1 opgave" and "3 opgaver" are both one lookup.
 */
export function plural(n, oneKey, manyKey) {
	return t(n === 1 ? oneKey : manyKey, { n });
}

/**
 * The BCP 47 tag for `Intl` — dates, times and numbers.
 *
 * The whole reason this exists: `toLocaleDateString('da-DK', …)` was written into
 * a dozen components, so an English interface would still have said "24. aug." and
 * started its weeks wherever da-DK does. One place to ask, so there is one place
 * to be wrong.
 *
 * `en-GB` rather than `en-US`: the app is day-month everywhere else, weeks start
 * on Monday, and an American date format inside a European layout reads as a bug.
 */
export function tag() {
	return i18n.locale === 'en' ? 'en-GB' : 'da-DK';
}
