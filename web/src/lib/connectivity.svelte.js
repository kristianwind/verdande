/**
 * Connectivity, as the app experiences it — not as the browser reports it.
 *
 * This is for short drops: a few seconds of lost Wi-Fi, a tunnel, a base station
 * handing over. It is deliberately NOT offline support. The app is already loaded
 * and its data is already in memory, so there is nothing to cache and nothing to
 * open from cold; what a blip needs is for the save you just made to survive it,
 * not for the whole app to run without a server.
 *
 * The request layer drives this: it retries a failed request for a short window
 * (see api.js) and tells this store while it is trying, so the interface can say
 * "reconnecting" rather than reverting the change under the user's hands. If the
 * window runs out it is a real outage, and then the normal error path takes over.
 *
 * `navigator.onLine` is only a hint — it goes false when the machine has no route,
 * but it stays true for a dead gateway or a server that is down, so it cannot be
 * the source of truth. It is used here only to wake a waiting retry the moment the
 * OS sees a link again, which is the one thing it is reliable for.
 */
import { browser } from '$app/environment';

class Connectivity {
	/** 'online' — every recent request reached the server.
	 *  'retrying' — a request failed on the network and is being retried.
	 *  'lost' — a request gave up after the retry window; a real outage. */
	status = $state('online');

	/** A request reached the server. The network is up, whatever it was doing. */
	reachable() {
		this.status = 'online';
	}

	/** A request failed on the network and is about to be tried again. */
	retrying() {
		if (this.status !== 'lost') this.status = 'retrying';
	}

	/** A request used up its retry window without reaching the server. */
	lost() {
		this.status = 'lost';
	}
}

export const connectivity = new Connectivity();

if (browser) {
	// The OS lost its link: say so at once rather than waiting for the next request
	// to fail. It does not, on its own, mean the server is unreachable — but with no
	// route there is nothing a save can do but wait, and the honest thing is to show
	// it waiting.
	window.addEventListener('offline', () => connectivity.retrying());
}

/**
 * Sleeps for `ms`, but wakes early the moment the OS reports a link again — so a
 * retry that is biding its time does not sit out the last two seconds of a blip
 * that has already ended.
 */
export function sleepOrOnline(ms) {
	return new Promise((resolve) => {
		let timer;
		const done = () => {
			clearTimeout(timer);
			if (browser) window.removeEventListener('online', done);
			resolve();
		};
		timer = setTimeout(done, ms);
		if (browser) window.addEventListener('online', done, { once: true });
	});
}
