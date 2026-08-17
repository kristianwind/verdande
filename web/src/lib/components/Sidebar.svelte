<script>
	import { app } from '$lib/stores.svelte.js';
	import { api } from '$lib/api.js';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';

	let { onnavigate } = $props();

	let adding = $state(false);
	let newName = $state('');

	async function createProject(event) {
		event.preventDefault();
		const name = newName.trim();
		if (!name) return;
		newName = '';
		adding = false;
		const project = await app.createProject(name);
		if (project) goto(`/projekt/${project.id}`);
	}

	async function signOut() {
		await api.logout();
		location.href = '/';
	}

	let filters = $state([]);
	let labels = $state([]);

	$effect(() => {
		api.listFilters().then((r) => (filters = r.filters)).catch(() => {});
		api.listLabels().then((r) => (labels = r.labels)).catch(() => {});
	});

	let shared = $derived(app.projects.filter((p) => !p.is_inbox && p.shared));
	let own = $derived(app.projects.filter((p) => !p.is_inbox && !p.shared));
	let current = $derived($page.url.pathname);
</script>

<nav class="sidebar" aria-label="Hovedmenu">
	<div class="brand">
		<!-- Verdande's mark: the rune Wunjo, which is the letter the Norn's name
		     starts with in the elder futhark. One glyph, no wordmark beside it —
		     the name is in the tab and everywhere else already. -->
		<span class="rune" aria-hidden="true">ᚹ</span>
		<span class="name">verdande</span>
	</div>

	<div class="views">
		<a href="/" class:active={current === '/'} onclick={onnavigate}>
			<span class="dot today" aria-hidden="true"></span>
			I dag
		</a>
		<a href="/upcoming" class:active={current === '/upcoming'} onclick={onnavigate}>
			<span class="dot" aria-hidden="true"></span>
			Kommende
		</a>
		{#if app.inbox}
			<a
				href="/projekt/{app.inbox.id}"
				class:active={current === `/projekt/${app.inbox.id}`}
				onclick={onnavigate}
			>
				<span class="dot" aria-hidden="true"></span>
				{app.inbox.name}
			</a>
		{/if}
	</div>

	<div class="group">
		<div class="group-head">
			<h2>Projekter</h2>
			<button onclick={() => (adding = !adding)} aria-label="Nyt projekt">+</button>
		</div>

		{#if adding}
			<form onsubmit={createProject}>
				<!-- svelte-ignore a11y_autofocus -->
				<input
					bind:value={newName}
					autofocus
					placeholder="Projektnavn"
					aria-label="Projektnavn"
					onblur={() => !newName.trim() && (adding = false)}
					onkeydown={(e) => e.key === 'Escape' && (adding = false)}
				/>
			</form>
		{/if}

		{#each own as project (project.id)}
			<a
				href="/projekt/{project.id}"
				class:active={current === `/projekt/${project.id}`}
				onclick={onnavigate}
			>
				<span class="dot" aria-hidden="true"></span>
				{project.name}
			</a>
		{/each}

		{#if own.length === 0 && !adding}
			<p class="empty">Ingen projekter endnu.</p>
		{/if}
	</div>

	{#if shared.length}
		<div class="group">
			<div class="group-head"><h2>Delt med mig</h2></div>
			{#each shared as project (project.id)}
				<a
					href="/projekt/{project.id}"
					class:active={current === `/projekt/${project.id}`}
					onclick={onnavigate}
				>
					<span class="dot" aria-hidden="true"></span>
					{project.name}
					<span class="count">{project.member_count}</span>
				</a>
			{/each}
		</div>
	{/if}

	{#if filters.length}
		<div class="group">
			<div class="group-head"><h2>Filtre</h2></div>
			{#each filters as filter (filter.id)}
				<a
					href="/filter/{filter.id}"
					class:active={current === `/filter/${filter.id}`}
					onclick={onnavigate}
				>
					<span class="dot" aria-hidden="true"></span>
					{filter.name}
				</a>
			{/each}
		</div>
	{/if}

	{#if labels.length}
		<div class="group">
			<div class="group-head"><h2>Etiketter</h2></div>
			{#each labels.filter((l) => l.task_count > 0) as label (label.id)}
				<a
					href="/etiket/{encodeURIComponent(label.name)}"
					class:active={current === `/etiket/${encodeURIComponent(label.name)}`}
					onclick={onnavigate}
				>
					<span class="dot" aria-hidden="true"></span>
					{label.name}
					<span class="count">{label.task_count}</span>
				</a>
			{/each}
		</div>
	{/if}

	<div class="foot">
		<!-- A quiet indicator, not an alarm: losing the socket for a moment is
		     normal, and the app keeps working without it. -->
		{#if !app.connected}
			<span class="offline" title="Ændringer fra andre vises ikke lige nu">Offline</span>
		{/if}
		<button class="user" onclick={signOut}>
			<span class="avatar" style="background: {app.user?.avatar_color}">
				{app.user?.name?.[0]?.toUpperCase() ?? '?'}
			</span>
			<span class="user-name">{app.user?.name}</span>
			<span class="signout">Log ud</span>
		</button>
	</div>
</nav>

<style>
	.sidebar {
		width: var(--sidebar-width);
		flex: none;
		display: flex;
		flex-direction: column;
		gap: var(--s5);
		padding: var(--s4) var(--s3);
		background: var(--surface-sunken);
		border-right: 1px solid var(--line);
		overflow-y: auto;
		padding-left: max(var(--s3), env(safe-area-inset-left));
	}

	.brand {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: 0 var(--s2);
	}

	.rune {
		font-size: var(--text-xl);
		color: var(--accent);
		line-height: 1;
	}

	.name {
		font-size: var(--text-lg);
		font-weight: 560;
		letter-spacing: -0.02em;
	}

	.views,
	.group {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.group-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0 var(--s2) var(--s2);
	}

	h2 {
		font-size: var(--text-xs);
		font-weight: 560;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
	}

	.group-head button {
		width: 20px;
		height: 20px;
		display: grid;
		place-items: center;
		border-radius: var(--radius-sm);
		color: var(--ink-faint);
		font-size: var(--text-lg);
		line-height: 1;
	}

	.group-head button:hover {
		color: var(--ink);
		background: var(--surface-raised);
	}

	a {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s2) var(--s2);
		border-radius: var(--radius);
		color: var(--ink-muted);
		text-decoration: none;
		font-size: var(--text-sm);
		transition:
			background var(--fast) var(--ease),
			color var(--fast) var(--ease);
	}

	a:hover {
		background: var(--surface);
		color: var(--ink);
	}

	a.active {
		background: var(--surface-raised);
		color: var(--ink);
		font-weight: 500;
	}

	.dot {
		width: 6px;
		height: 6px;
		border-radius: var(--radius-full);
		background: var(--line-strong);
		flex: none;
	}

	a.active .dot,
	.dot.today {
		background: var(--accent);
	}

	.count {
		margin-left: auto;
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.empty {
		margin: 0;
		padding: var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-faint);
	}

	form input {
		width: 100%;
		padding: var(--s2);
		background: var(--surface);
		border: 1px solid var(--accent);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	.foot {
		margin-top: auto;
		padding-top: var(--s4);
		border-top: 1px solid var(--line);
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	.offline {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding: 0 var(--s2);
	}

	.user {
		display: flex;
		align-items: center;
		gap: var(--s2);
		padding: var(--s2);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		width: 100%;
	}

	.user:hover {
		background: var(--surface);
	}

	.avatar {
		width: 22px;
		height: 22px;
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		font-size: var(--text-xs);
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	.user-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.signout {
		margin-left: auto;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		opacity: 0;
		transition: opacity var(--fast) var(--ease);
	}

	.user:hover .signout {
		opacity: 1;
	}
</style>
