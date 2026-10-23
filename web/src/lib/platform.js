/**
 * Where the interface is running, and what that changes about reaching the server.
 *
 * In a browser the interface and the API share an origin: every path is relative
 * and the session is an httpOnly cookie no script can see. In the iOS app the
 * interface is bundled into the app and served from capacitor://localhost, and
 * the server is whichever one its owner typed in. So the app needs three things
 * a browser never does: the server's address, its own session token, and that
 * token on every request instead of a cookie.
 *
 * Everything that differs between the two lives here, so `api.js` and the socket
 * ask one question — `apiURL(path)`, `authorize(init)` — and the browser's answer
 * is exactly what it was before this file existed.
 */

/** True inside the iOS app. Capacitor puts its bridge on `window` before any of
 *  our code runs. */
export function isNativeApp() {
	return !!globalThis.Capacitor?.isNativePlatform?.();
}

/**
 * The app's stored settings: which server, and the session on it.
 *
 * localStorage for now, behind this one object, so the Keychain plugin can take
 * its place without anything else changing. The token is a session — whoever
 * holds it is signed in — and the Keychain is where a phone keeps those; until it
 * is wired in, this is a WKWebView's own storage, readable only by the app.
 */
const SERVER_KEY = 'verdande.app.server';
const TOKEN_KEY = 'verdande.app.session';

function read(key) {
	try {
		return localStorage.getItem(key) ?? '';
	} catch {
		return '';
	}
}

function write(key, value) {
	try {
		if (value) localStorage.setItem(key, value);
		else localStorage.removeItem(key);
	} catch {
		// Storage refused (private mode, quota). The app then asks again next
		// launch, which is the right failure: a token that seemed saved and was
		// not would look like being signed out for no reason.
	}
}

export const appSession = {
	server: () => read(SERVER_KEY),
	setServer: (url) => write(SERVER_KEY, url),
	token: () => read(TOKEN_KEY),
	setToken: (token) => write(TOKEN_KEY, token),
	/** Signed out: the token goes, the server stays — signing in again is to the
	 *  same place nearly every time. */
	signOut: () => write(TOKEN_KEY, '')
};

/** The address of an API path: relative in a browser, on the chosen server in
 *  the app. */
export function apiURL(path) {
	const base = isNativeApp() ? appSession.server() : '';
	return `${base}/api/v1${path}`;
}

/**
 * Fills in how a request proves who is asking.
 *
 * A browser sends its cookie, same-origin only. The app sends no cookie at all —
 * `omit`, so nothing the WebView picked up from somewhere rides along — and its
 * token as a bearer.
 */
export function authorize(init) {
	if (!isNativeApp()) {
		init.credentials = 'same-origin';
		return init;
	}
	init.credentials = 'omit';
	const token = appSession.token();
	if (token) {
		init.headers = { ...(init.headers ?? {}), Authorization: `Bearer ${token}` };
	}
	return init;
}

/** What the app calls itself in the list of signed-in devices. */
export function deviceName() {
	const ua = navigator.userAgent;
	if (/iPad/.test(ua)) return 'iPad';
	if (/iPhone/.test(ua)) return 'iPhone';
	return 'Verdande-appen';
}

/**
 * Normalises what somebody typed as their server's address.
 *
 * "verdande.example.dk" becomes "https://verdande.example.dk"; a trailing slash
 * and a pasted "/api/v1" are dropped. Plain http is kept when typed on purpose,
 * because a self-hoster on their own network may have no certificate — iOS will
 * refuse it unless the app allows it, and that refusal should be the one they see.
 */
export function normaliseServer(input) {
	let s = (input ?? '').trim();
	if (!s) return '';
	if (!/^https?:\/\//i.test(s)) s = `https://${s}`;
	try {
		const u = new URL(s);
		return `${u.protocol}//${u.host}`;
	} catch {
		return '';
	}
}
