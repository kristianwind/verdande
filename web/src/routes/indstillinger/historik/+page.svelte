<script>
	/**
	 * Historik: what has been done on this instance, across every project.
	 *
	 * The other half of Fejl. That page is what broke; this is what was done, and an
	 * administrator had nowhere to ask the second question: the per-project log
	 * takes one project id, and an administrator is not necessarily a member of the
	 * project they need to look into.
	 *
	 * Paged with the cursor the server hands back rather than a page number. The log
	 * is written to constantly and read rarely, so a second page fetched a minute
	 * after the first would otherwise be shifted by everything written in between —
	 * one row shown twice and the row behind it skipped, which is the one failure an
	 * audit log must not have.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app, projectName } from '$lib/stores.svelte.js';
	import { eventName, eventDetail } from '$lib/events.js';
	import { t } from '$lib/i18n.svelte.js';
	import { ago } from '$lib/when.js';

	let entries = $state([]);
	let cursor = $state(null);
	let loading = $state(false);
	let loaded = $state(false);

	// The filters. Events come from the server rather than from a list here: the
	// names are an internal vocabulary, and a select built from what actually
	// occurred cannot offer an option that returns nothing.
	let events = $state([]);
	let people = $state([]);
	let projects = $state([]);
	let filterUser = $state('');
	let filterProject = $state('');
	let filterEvent = $state('');

	$effect(() => {
		Promise.all([api.auditEvents(), api.listUsers(), api.listProjects()])
			.then(([e, u, p]) => {
				events = e.events;
				people = u.users;
				projects = p.projects;
			})
			.catch((error) => app.toast(humanMessage(error)));
	});

	// Reloads from the top whenever a filter changes. Reading the three filters here
	// is what subscribes the effect to them.
	$effect(() => {
		const query = {
			user_id: filterUser,
			project_id: filterProject,
			event: filterEvent,
			limit: 50
		};
		let cancelled = false;
		loading = true;
		api
			.auditLog(query)
			.then((r) => {
				if (cancelled) return;
				entries = r.activity;
				cursor = r.next_cursor ?? null;
				loaded = true;
			})
			.catch((error) => {
				if (!cancelled) app.toast(humanMessage(error));
			})
			.finally(() => {
				if (!cancelled) loading = false;
			});
		// A filter changed while a page was in flight: the late answer belongs to the
		// old filter and must not land on top of the new one.
		return () => {
			cancelled = true;
		};
	});

	async function more() {
		if (!cursor || loading) return;
		loading = true;
		try {
			const r = await api.auditLog({
				before: cursor,
				user_id: filterUser,
				project_id: filterProject,
				event: filterEvent,
				limit: 50
			});
			entries = [...entries, ...r.activity];
			cursor = r.next_cursor ?? null;
		} catch (error) {
			app.toast(humanMessage(error));
		} finally {
			loading = false;
		}
	}


	let filtered = $derived(Boolean(filterUser || filterProject || filterEvent));

	function clearFilters() {
		filterUser = '';
		filterProject = '';
		filterEvent = '';
	}
</script>

<section class="panel">
	<header>
		<h2>{t('history.title')}</h2>
		<p class="hint">
			{t('history.hint')}
		</p>
	</header>

	<div class="filters">
		<label>
			<span>{t('history.person')}</span>
			<select bind:value={filterUser}>
				<option value="">{t('history.all')}</option>
				{#each people as person (person.id)}
					<option value={person.id}>{person.name}</option>
				{/each}
			</select>
		</label>

		<label>
			<span>{t('history.project')}</span>
			<select bind:value={filterProject}>
				<option value="">{t('history.all')}</option>
				{#each projects as project (project.id)}
					<option value={project.id}>{projectName(project)}</option>
				{/each}
			</select>
		</label>

		<label>
			<span>{t('history.event')}</span>
			<select bind:value={filterEvent}>
				<option value="">{t('history.all')}</option>
				{#each events as event (event.event)}
					<option value={event.event}>{eventName(event.event)} ({event.count})</option>
				{/each}
			</select>
		</label>

		{#if filtered}
			<button class="secondary" onclick={clearFilters}>{t('history.clear')}</button>
		{/if}
	</div>

	{#if entries.length}
		<ul class="rows">
			{#each entries as entry (entry.id)}
				<li>
					<div class="what">
						<span class="primary-line">
							<span class="who" class:gone={!entry.user_name}
								>{entry.user_name || t('history.deletedAccount')}</span
							>
							<span class="event"
								>{eventName(entry.event)}{#if eventDetail(entry)}
									<span class="detail">{eventDetail(entry)}</span>{/if}</span
							>
						</span>
						<span class="secondary">{entry.project_name}</span>
					</div>
					<span class="when">{ago(entry.at)}</span>
				</li>
			{/each}
		</ul>

		{#if cursor}
			<div class="row">
				<button class="secondary" onclick={more} disabled={loading}>
					{loading ? t('history.loading') : t('history.more')}
				</button>
			</div>
		{/if}
	{:else if loaded}
		<p class="hint">
			{filtered
				? t('history.noMatch')
				: t('history.nothing')}
		</p>
	{/if}
</section>

<style>
	/* The filters wrap rather than scroll: three selects on a phone are three lines,
	   and a select that has scrolled halfway out of view is a filter you cannot see
	   the state of. */
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		gap: var(--s3);
	}

	.filters label {
		display: flex;
		flex-direction: column;
		gap: var(--s1);
		font-size: var(--text-sm);
		color: var(--ink-muted);
		min-width: 0;
		flex: 1 1 10rem;
	}

	.filters select {
		width: 100%;
		padding: var(--s2) var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		outline: none;
	}

	.filters select:focus {
		border-color: var(--accent);
	}

	.rows {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}

	.rows li {
		display: flex;
		align-items: flex-start;
		gap: var(--s3);
		padding: var(--s3) 0;
		border-bottom: 1px solid var(--line);
	}

	.rows li:last-child {
		border-bottom: 0;
	}

	.what {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.primary-line {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: var(--s2);
		font-size: var(--text-sm);
	}

	.who {
		font-weight: 500;
	}

	/* A name that is not a name reads at the weight of the surrounding prose, not at
	   the weight a person's name gets. */
	.who.gone {
		font-weight: 400;
		font-style: italic;
		color: var(--ink-faint);
	}

	.event {
		color: var(--ink-muted);
	}

	.detail {
		color: var(--ink);
	}

	.secondary {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		overflow-wrap: anywhere;
	}

	.when {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		flex: none;
	}
</style>
