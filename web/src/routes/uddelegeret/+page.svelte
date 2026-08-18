<script>
	/**
	 * Venter på andre: what you have handed to somebody else and are waiting for.
	 *
	 * Grouped by person, because that is the question — "what is Anders sitting
	 * on" — rather than by date. The server groups it and sends the names with it;
	 * a client has no way to ask for the name behind an assignee id it has not met
	 * in a project it happens to have open.
	 */
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import { t } from '$lib/i18n.svelte.js';

	let people = $state([]);
	let loaded = $state(false);

	$effect(() => {
		api.delegated().then((data) => {
			people = data.people;
			app.tasks = data.people.flatMap((p) => p.tasks);
			loaded = true;
		});
	});

	// Read back out of the store, so a task that is closed or reassigned from the
	// drawer leaves the list at once rather than on the next load. Reassigning to
	// yourself is the interesting one: it stops being delegated the moment it
	// happens, which is exactly what this view should show.
	const live = (person) =>
		app.tasks.filter((t) => t.assignee_id === person.user_id && !t.completed);
</script>

<div class="view">
	<header><h1>{t('view.delegated')}</h1></header>

	{#each people as person (person.user_id)}
		{@const tasks = live(person)}
		{#if tasks.length}
			<section>
				<h2>
					<span class="avatar" style="background: {person.avatar_color}">
						{person.name[0]?.toUpperCase() ?? '?'}
					</span>
					{person.name}
					<span class="count">{tasks.length}</span>
				</h2>
				{#each tasks as task (task.id)}
					<TaskRow {task} />
				{/each}
			</section>
		{/if}
	{/each}

	{#if loaded && !people.some((p) => live(p).length)}
		<p class="clear">
			<span class="rune" aria-hidden="true">ᚹ</span>
			{t('view.delegatedEmpty')}
		</p>
	{/if}
</div>

<style>
	.view {
		max-width: var(--content-max);
		margin: 0 auto;
		padding: var(--s6) var(--s4) var(--s8);
	}

	h1 {
		font-size: var(--text-2xl);
		margin-bottom: var(--s5);
	}

	section {
		margin-top: var(--s5);
	}

	/* The person is the heading, so their face is part of it rather than a
	   decoration beside it. */
	h2 {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
		font-weight: 560;
		color: var(--ink-muted);
		padding: 0 var(--s2) var(--s2);
		border-bottom: 1px solid var(--line);
		margin-bottom: var(--s2);
	}

	.avatar {
		width: 20px;
		height: 20px;
		border-radius: var(--radius-full);
		display: grid;
		place-items: center;
		font-size: var(--text-xs);
		font-weight: 560;
		color: #fff;
		flex: none;
	}

	.count {
		margin-left: auto;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		font-weight: 400;
	}

	.clear {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--s3);
		padding: var(--s8) var(--s4);
		color: var(--ink-faint);
		font-size: var(--text-sm);
	}

	.rune {
		font-size: var(--text-2xl);
		color: var(--accent);
		opacity: 0.5;
	}
</style>
