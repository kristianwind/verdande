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
		</div>
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
