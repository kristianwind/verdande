/// <reference types="@sveltejs/kit" />
/* global self, caches, fetch, Response */

/**
 * verdande's service worker: Web Push, and a cache that holds through a short
 * network drop.
 *
 * It used to do Push and nothing else, with a note that a cache serving yesterday's
 * data was worse than an honest "cannot connect". The cache here does not do that.
 * It is network-FIRST for everything that can go stale — a reader online always gets
 * the live answer, and the cache is only reached for when the network is not — so it
 * is not a copy of the app you work in offline, it is the app surviving a tunnel, a
 * lift, a base-station handover. The five themes' worth of live task list stays live;
 * it just no longer goes blank the moment the signal does.
 *
 * Writes are never touched here — they go straight to the network, and api.js already
 * retries them across a short drop. Caching a write would mean a queue and conflicts,
 * which is a different and much larger feature.
 */
import { build, files, version } from '$service-worker';

// Versioned, so a deploy's worth of new hashed assets lands in a fresh cache and the
// old one is dropped on activate — the app can never be served half of two builds.
const SHELL = `verdande-shell-${version}`;
// Not versioned: the data does not belong to a build, and clearing it every deploy
// would throw away the very copy that lets a reload survive a drop. Cleared on sign-out.
const API = 'verdande-api';

// The shell: every hashed build asset, the static files, and the SPA fallback so a
// navigation can be answered from the cache when the network cannot answer it.
const SHELL_ASSETS = [...build, ...files, '/'];

self.addEventListener('install', (event) => {
	event.waitUntil(
		(async () => {
			const cache = await caches.open(SHELL);
			await cache.addAll(SHELL_ASSETS);
			// Take over at the next opportunity rather than waiting for every tab to
			// close — paired with clients.claim below and the versioned cache, a deploy
			// reaches the user on their next load.
			await self.skipWaiting();
		})()
	);
});

self.addEventListener('activate', (event) => {
	event.waitUntil(
		(async () => {
			for (const key of await caches.keys()) {
				// Drop old shell versions; keep this shell and the data cache.
				if (key !== SHELL && key !== API) await caches.delete(key);
			}
			await self.clients.claim();
		})()
	);
});

const isAsset = (pathname) => build.includes(pathname) || files.includes(pathname);

self.addEventListener('fetch', (event) => {
	const { request } = event;

	// Writes go to the network, full stop — the app retries them itself, and a cached
	// write is a promise this cannot keep. Cross-origin is none of our business.
	if (request.method !== 'GET') return;
	const url = new URL(request.url);
	if (url.origin !== self.location.origin) return;

	// Hashed build assets and static files never change for a given URL: cache-first,
	// so a repeat load and an offline load are both instant.
	if (isAsset(url.pathname)) {
		event.respondWith(cacheFirst(request, SHELL));
		return;
	}

	// The data: network-first so an online reader always sees the live list, with the
	// last good copy kept for when the network is gone.
	if (url.pathname.startsWith('/api/v1/')) {
		event.respondWith(networkFirst(request, API));
		return;
	}

	// A navigation (reload, or opening the app): the live page when there is one, the
	// cached shell when there is not — so the app opens through a drop instead of
	// showing the browser's dinosaur.
	if (request.mode === 'navigate') {
		event.respondWith(navigate(request));
		return;
	}
});

async function cacheFirst(request, cacheName) {
	const cache = await caches.open(cacheName);
	const hit = await cache.match(request);
	if (hit) return hit;
	const res = await fetch(request);
	if (res.ok) cache.put(request, res.clone());
	return res;
}

async function networkFirst(request, cacheName) {
	const cache = await caches.open(cacheName);
	try {
		const res = await fetch(request);
		// Only a real answer is worth keeping — a 401 or a 500 must not overwrite the
		// last good copy, or an expired session would be served back offline as fact.
		if (res.ok) cache.put(request, res.clone());
		return res;
	} catch (err) {
		const hit = await cache.match(request);
		if (hit) return hit;
		throw err;
	}
}

async function navigate(request) {
	try {
		return await fetch(request);
	} catch {
		const cache = await caches.open(SHELL);
		const shell = await cache.match('/');
		return shell ?? Response.error();
	}
}

// Sign-out asks for the data cache to be emptied: the next person on this browser must
// not find the last one's tasks sitting in it.
self.addEventListener('message', (event) => {
	if (event.data?.type === 'clear-api-cache') {
		event.waitUntil(caches.delete(API));
	}
});

// --- Web Push (unchanged in behaviour, moved here from the old static/sw.js) --------

self.addEventListener('push', (event) => {
	let payload = {};
	try {
		payload = event.data ? event.data.json() : {};
	} catch {
		// A push whose body is not our JSON still deserves to be shown: browsers show
		// their own "site updated in the background" notice otherwise, which looks like
		// a bug in the app.
		payload = { title: 'verdande', body: event.data ? event.data.text() : '' };
	}

	const title = payload.title || 'verdande';
	event.waitUntil(
		self.registration.showNotification(title, {
			body: payload.body || '',
			icon: '/icon.svg',
			badge: '/icon.svg',
			// The tag collapses repeats: five comments on one task should replace each
			// other rather than stack into five separate notifications.
			tag: payload.tag || payload.url || 'verdande',
			data: { url: payload.url || '/' }
		})
	);
});

self.addEventListener('notificationclick', (event) => {
	event.notification.close();
	const target = event.notification.data?.url || '/';

	event.waitUntil(
		(async () => {
			const clientList = await self.clients.matchAll({
				type: 'window',
				includeUncontrolled: true
			});

			// Focus a tab that already has the app open rather than opening a second one.
			// Somebody with the app pinned does not want a new tab per reminder.
			for (const client of clientList) {
				if (new URL(client.url).origin === self.location.origin) {
					await client.focus();
					if ('navigate' in client) await client.navigate(target);
					return;
				}
			}
			await self.clients.openWindow(target);
		})()
	);
});
