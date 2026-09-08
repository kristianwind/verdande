/**
 * Mærket på appens ikon — tallet i Dock'en på en Mac, på proceslinjen på en pc.
 *
 * Klokken i hovedet siger det samme, men kun til den, der kigger på fanen. Det
 * halve af pointen med at få besked er at få den, mens man laver noget andet, og
 * det eneste sted, appen kan sige noget dér, er på sit eget ikon.
 *
 * Sat to steder, fordi der er to tilstande: her, mens appen er åben og selv ved
 * hvor mange ulæste der er, og i service-workeren, når den ikke er — se `push`-
 * lytteren i service-worker.js, som får tallet med i beskeden.
 *
 * Ét tal, ikke en optælling. Mærket sættes til hvad der *er*, aldrig til hvad der
 * lige er kommet, så det kommer ned igen af sig selv, når beskederne bliver læst.
 */

/**
 * Kan browseren mærke sit ikon? Safari og Chrome kan, når appen er lagt i Dock'en
 * eller installeret; en almindelig fane har intet ikon at mærke, og kaldet gør
 * ingenting. Firefox kan ikke endnu.
 */
export function supported() {
	return typeof navigator !== 'undefined' && 'setAppBadge' in navigator;
}

/**
 * Sætter mærket til `n`, eller fjerner det ved nul.
 *
 * Tavs, når det ikke kan lade sig gøre. Et mærke er en venlighed oven på klokken,
 * der allerede står i fladen — ikke noget at afbryde nogen med, hvis browseren
 * siger nej.
 */
export function setBadge(n) {
	if (!supported()) return;
	const count = Number(n) || 0;
	try {
		const done = count > 0 ? navigator.setAppBadge(count) : navigator.clearAppBadge();
		done?.catch?.(() => {});
	} catch {
		// Nogle browsere kaster i stedet for at afvise. Det er det samme svar.
	}
}

/** Fjerner mærket helt. Brugt ved log ud: den næste, der lander her, er en anden. */
export function clearBadge() {
	setBadge(0);
}
