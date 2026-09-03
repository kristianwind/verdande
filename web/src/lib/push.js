/**
 * Web Push, from the browser's side.
 *
 * Three parties have to agree before a notification arrives: the browser (which
 * needs permission and a registered service worker), the push service it chooses
 * (Google's, Mozilla's), and this server (which holds the VAPID pair and does the
 * RFC 8291 encryption). This module is the first of those, and nothing else.
 */

import { api } from './api.js';
import { t } from './i18n.svelte.js';

/** Whether this browser can do it at all. Safari before 16.4 cannot, and neither
 *  can any browser on a page served over plain HTTP from a non-localhost host. */
export function supported() {
	return (
		typeof window !== 'undefined' &&
		'serviceWorker' in navigator &&
		'PushManager' in window &&
		'Notification' in window
	);
}

export function permission() {
	return supported() ? Notification.permission : 'unsupported';
}

async function registration() {
	// SvelteKit registers src/service-worker.js itself on load, so normally there
	// is already a registration here. Register it by hand only if there is not yet
	// one — a push subscribe that races the app's own registration would otherwise
	// find nothing. `ready` waits for an active worker; register() alone resolves
	// while the worker is still installing, and subscribing against that fails.
	const existing = await navigator.serviceWorker.getRegistration();
	if (!existing) await navigator.serviceWorker.register('/service-worker.js');
	return navigator.serviceWorker.ready;
}

/** The current subscription, or null. Cheap: it reads local state, no network. */
export async function current() {
	if (!supported()) return null;
	try {
		const reg = await navigator.serviceWorker.getRegistration();
		return (await reg?.pushManager.getSubscription()) ?? null;
	} catch {
		return null;
	}
}

/**
 * Asks permission, subscribes, and tells the server.
 *
 * Throws with a message meant to be shown. The failures here are not the API's
 * error shape — a refused permission prompt is not an HTTP status — so they are
 * plain Errors with Danish text.
 */
export async function subscribe() {
	if (!supported()) throw new Error('Denne browser kan ikke vise notifikationer.');

	const granted = await Notification.requestPermission();
	if (granted !== 'granted') {
		throw new Error(
			granted === 'denied'
				? t('push.deniedByBrowser')
				: t('push.notEnabled')
		);
	}

	const reg = await registration();
	const { public_key } = await api.pushKey();
	if (!public_key) throw new Error(t('push.noVapidKey'));

	const subscription = await reg.pushManager.subscribe({
		// Not optional in Chrome: a subscription that is not userVisibleOnly is
		// refused outright.
		userVisibleOnly: true,
		applicationServerKey: urlBase64ToUint8Array(public_key)
	});

	const raw = subscription.toJSON();
	await api.subscribePush({
		endpoint: raw.endpoint,
		keys: { p256dh: raw.keys.p256dh, auth: raw.keys.auth }
	});
	return subscription;
}

/** Unsubscribes here and forgets it on the server. */
export async function unsubscribe() {
	const subscription = await current();
	if (!subscription) return;

	// Server first: if the browser unsubscribes and then the request fails, the
	// server keeps pushing to an endpoint nothing is listening on, and only a
	// 410 from the push service will ever clean it up.
	await api.unsubscribePush(subscription.endpoint);
	await subscription.unsubscribe();
}

/**
 * The VAPID public key travels as base64url text and `applicationServerKey` wants
 * raw bytes. Neither `atob` nor the browser does this conversion — every Web Push
 * client on the internet carries some version of these six lines.
 */
function urlBase64ToUint8Array(base64url) {
	const padding = '='.repeat((4 - (base64url.length % 4)) % 4);
	const base64 = (base64url + padding).replace(/-/g, '+').replace(/_/g, '/');
	const raw = atob(base64);

	const bytes = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) bytes[i] = raw.charCodeAt(i);
	return bytes;
}
