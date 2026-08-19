<script>
	/**
	 * A task, by address.
	 *
	 * There was no such thing until a note wanted to point at one. Opening a task
	 * shows a drawer over whatever list you were on and changes nothing in the URL,
	 * which is right for a click and useless for a link — you cannot send somebody a
	 * task, and a note that mentions one has nowhere to send them either.
	 *
	 * So this opens the drawer and then steps aside, replacing itself with the
	 * task's own project. The person lands where the task lives, with it open, and
	 * the back button does not walk them through a page that only ever redirected.
	 */
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';

	let status = $state('loading');

	$effect(() => {
		open($page.params.id);
	});

	async function open(id) {
		if (!id) return;
		try {
			const task = await api.getTask(id);
			app.openDetail(id);
			// replaceState, so Back goes where they came from rather than here.
			await goto(task.project_id ? `/projekt/${task.project_id}` : '/', { replaceState: true });
		} catch {
			// 404 for both "no such task" and "not yours", so an id tells a stranger
			// nothing about whether it exists.
			status = 'missing';
		}
	}
</script>

{#if status === 'missing'}
	<p class="clear">{t('detail.notFound')}</p>
{/if}

<style>
	.clear {
		padding: var(--s4);
		color: var(--ink-faint);
	}
</style>
