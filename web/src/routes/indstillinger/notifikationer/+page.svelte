<script>
	/** Notifications: what has happened, and whether the browser should tell you. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import * as push from '$lib/push.js';
	import { t, plural } from '$lib/i18n.svelte.js';

	// --- web push ----------------------------------------------------------------

	let pushState = $state('loading');
	let busy = $state(false);

	$effect(() => {
		if (!push.supported()) {
			pushState = 'unsupported';
			return;
		}
		if (push.permission() === 'denied') {
			pushState = 'blocked';
			return;
		}
		push.current().then((sub) => (pushState = sub ? 'on' : 'off'));
	});

	async function enablePush() {
		busy = true;
		try {
			await push.subscribe();
			pushState = 'on';
		} catch (e) {
			app.toast(e.message ?? humanMessage(e));
			if (push.permission() === 'denied') pushState = 'blocked';
		} finally {
			busy = false;
		}
	}

	let testing = $state(false);
	let testResult = $state(null);

	/**
	 * Én notifikation, hele vejen gennem den rigtige rute, og et svar på hvad der
	 * skete med den.
	 *
	 * Den siger ikke "sendt" og lader det være. Kommer den frem, ved man det, fordi
	 * den ligger på skærmen; kommer den ikke, står der her, hvem der afviste den og
	 * med hvilke ord — og hvis ingen afviste den, står der, at det så er browseren
	 * eller styresystemet, der holder den tilbage. Det er de tre steder, den kan
	 * blive væk, og nu peger svaret på ét af dem.
	 */
	async function testPush() {
		testing = true;
		testResult = null;
		try {
			const r = await api.testPush(t('push.testTitle'), t('push.testBody'));
			if (!r.subscriptions) {
				testResult = { bad: true, text: t('push.testNone') };
			} else if (r.failed?.length) {
				testResult = {
					bad: true,
					text: r.failed
						.map((f) => t('push.testFailed', { service: f.service, reason: f.reason }))
						.join(' ')
				};
			} else {
				testResult = { bad: false, text: t('push.testSent', { n: r.sent }) };
			}
		} catch (e) {
			testResult = { bad: true, text: humanMessage(e) };
		} finally {
			testing = false;
		}
	}

	async function disablePush() {
		busy = true;
		try {
			await push.unsubscribe();
			pushState = 'off';
		} catch (e) {
			app.toast(e.message ?? humanMessage(e));
		} finally {
			busy = false;
		}
	}

	// --- the list -----------------------------------------------------------------

	let notifications = $state([]);
	let unread = $state(0);
	let loading = $state(true);

	$effect(() => {
		api
			.listNotifications()
			.then((r) => {
				notifications = r.notifications;
				unread = r.unread;
			})
			.catch((e) => app.toast(humanMessage(e)))
			.finally(() => (loading = false));
	});

	async function markAllRead() {
		const previous = notifications;
		notifications = notifications.map((n) => ({ ...n, read: true }));
		unread = 0;
		try {
			await api.markNotificationsRead();
		} catch (e) {
			notifications = previous;
			app.toast(humanMessage(e));
		}
	}

	function when(iso) {
		const then = new Date(iso);
		const minutes = Math.round((Date.now() - then) / 60000);
		if (minutes < 1) return 'lige nu';
		if (minutes < 60) return `${minutes} min. siden`;
		if (minutes < 60 * 24) return `${Math.round(minutes / 60)} t. siden`;
		return then.toLocaleDateString('da-DK', { day: 'numeric', month: 'short' });
	}

	// --- version ------------------------------------------------------------------

	let version = $state(null);

	// --- restarting from here ---------------------------------------------------

	let panel = $state(null);
	let restarting = $state(false);
	let restartNote = $state('');

	$effect(() => {
		api
			.panelStatus()
			.then((p) => (panel = p))
			.catch(() => {});
	});

	/**
	 * Asks the panel to recreate this container, then waits for it to come back.
	 *
	 * The reply may never arrive — the panel stops this container as part of
	 * answering — so the answer is not believed either way. `/healthz` is polled
	 * until it responds, which is the only thing that actually says "it is up".
	 */
	async function restart() {
		if (!confirm(t('update.restartQuestion'))) return;
		restarting = true;
		restartNote = t('update.asking');
		try {
			await api.restartFromPanel();
		} catch (e) {
			// A request cut off mid-flight is the successful case wearing a
			// failure's clothes. The poll below decides.
			restartNote = humanMessage(e);
		}

		restartNote = t('update.waiting');
		for (let i = 0; i < 60; i++) {
			await new Promise((r) => setTimeout(r, 2000));
			try {
				const response = await fetch('/healthz', { cache: 'no-store' });
				if (response.ok) {
					location.reload();
					return;
				}
			} catch {
				// Still down, which is expected for the first few seconds.
			}
		}
		restarting = false;
		restartNote = t('update.stillDown');
	}

	$effect(() => {
		api.version().then((v) => (version = v)).catch(() => {});
	});
</script>

<section class="panel">
	<header>
		<h2>{t('push.title')}</h2>
		<p class="hint">
			{t('push.hint')}
		</p>
	</header>

	{#if pushState === 'loading'}
		<p class="empty">…</p>
	{:else if pushState === 'unsupported'}
		<p class="hint">
			{t('push.unsupported')}
		</p>
	{:else if pushState === 'blocked'}
		<p class="hint">
			{t('push.blocked')}
		</p>
	{:else if pushState === 'on'}
		<p class="hint">{t('push.on')}</p>
		<div class="row">
			<button class="secondary" onclick={disablePush} disabled={busy}>{t('push.turnOff')}</button>
			<!-- "Til" er ikke det samme som "virker".
			     Ruden sagde til, så snart der var et abonnement, og en push, tjenesten
			     afviste, gik i en logline på Debug, som ingen ser. Tre forskellige
			     problemer — browseren tilmeldte sig aldrig, tjenesten afviste den,
			     der er ikke noget forfaldent endnu — så ens ud fra den her side. -->
			<button class="secondary" onclick={testPush} disabled={busy || testing}>
				{testing ? t('push.testing') : t('push.test')}
			</button>
		</div>
		{#if testResult}
			<p class="hint" class:bad={testResult.bad}>{testResult.text}</p>
		{/if}
	{:else}
		<div class="row">
			<button class="primary" onclick={enablePush} disabled={busy}>
				{t('push.turnOn')}
			</button>
		</div>
		<p class="hint">{t('push.perBrowser')}</p>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>{t('push.recent')}</h2>
		{#if unread > 0}
			<p class="hint">{plural(unread, 'push.unreadOne', 'push.unreadMany')}</p>
		{/if}
	</header>

	{#if loading}
		<p class="empty">…</p>
	{:else if notifications.length === 0}
		<p class="empty">{t('push.none')}</p>
	{:else}
		<ul class="list">
			{#each notifications as note (note.id)}
				<li class:unread={!note.read}>
					<div class="what">
						<span class="title">{note.title}</span>
						{#if note.body}<span class="body">{note.body}</span>{/if}
						<span class="meta">
							{#if note.actor_name}{note.actor_name} · {/if}{when(note.created_at)}
						</span>
					</div>
					{#if note.task_id && note.project_id}
						<a class="go" href="/projekt/{note.project_id}">{t('push.open')}</a>
					{/if}
				</li>
			{/each}
		</ul>

		{#if unread > 0}
			<div class="row">
				<button class="secondary" onclick={markAllRead}>{t('push.markAllRead')}</button>
			</div>
		{/if}
	{/if}
</section>

{#if version}
	<section class="panel">
		<header>
			<h2>{t('push.version')}</h2>
		</header>

		<p class="hint">
			{t('push.running')} <span class="mono">{version.current}</span>.
			{#if version.disabled}
				{t('push.updatesOff')}
			{:else if version.update_available}
				<strong>{t('push.updateOut', { version: version.latest })}</strong>
			{:else if version.checked_at}
				{t('push.upToDate')}
			{/if}
		</p>

		<!-- Restarting is what makes a new version arrive: a container cannot replace
		     its own image, and the panel pulls `:latest` when it recreates one. The
		     button is here rather than in another browser tab. -->
		{#if panel?.configured}
			<div class="row">
				<button class="secondary" onclick={restart} disabled={restarting}>
					{restarting ? t('update.restarting') : t('update.restart')}
				</button>
				{#if restartNote}<span class="saved">{restartNote}</span>{/if}
			</div>
		{:else if panel}
			<p class="hint">{t('update.notConfigured', { missing: panel.missing.join(', ') })}</p>
		{/if}

		{#if version.update_available}
			{#if version.notes}
				<p class="notes">{version.notes}</p>
			{/if}
			{#if version.url}
				<div class="row">
					<a class="release" href={version.url} target="_blank" rel="noreferrer noopener">
						{t('push.seeRelease')}
					</a>
				</div>
			{/if}
		{/if}
	</section>
{/if}

<style>
	.bad {
		color: var(--danger);
	}

	.what {
		display: flex;
		flex-direction: column;
		gap: 2px;
		flex: 1;
		min-width: 0;
	}

	.title {
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}

	.body {
		font-size: var(--text-sm);
		color: var(--ink-muted);
		overflow-wrap: anywhere;
	}

	.meta {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	/* An accent bar rather than a bold row: the list is read top to bottom, and
	   weight changes make it look ragged. */
	li.unread {
		border-left: 2px solid var(--accent);
	}

	.go,
	.release {
		flex: none;
		font-size: var(--text-xs);
		color: var(--accent);
		text-decoration: none;
	}

	.release {
		font-size: var(--text-sm);
	}

	.notes {
		margin: 0;
		padding: var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		white-space: pre-wrap;
		line-height: 1.6;
	}
</style>
