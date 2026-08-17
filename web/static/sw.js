/**
 * verdande's service worker.
 *
 * It exists for Web Push and nothing else. There is deliberately no offline cache:
 * the app is a live view of a task list that other people are editing, and a cached
 * shell serving yesterday's data is worse than a browser saying it cannot connect.
 * Adding one later means versioning and invalidating it, which is a real feature
 * rather than a few lines here.
 */

// Take over as soon as the browser is willing. Without these two, the first push
// after granting permission arrives with no worker in control to show it.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

self.addEventListener('push', (event) => {
	let payload = {};
	try {
		payload = event.data ? event.data.json() : {};
	} catch {
		// A push whose body is not our JSON still deserves to be shown: browsers
		// display their own "site updated in the background" notice otherwise, which
		// looks like a bug in the app.
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

			// Focus a tab that already has the app open rather than opening a second
			// one. Somebody with the app pinned does not want a new tab per reminder.
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
