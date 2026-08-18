<script>
	/**
	 * Fejl: what has gone wrong lately, kept where a restart cannot take it.
	 *
	 * The panel's watcher already says "HTTP 5xx, twice, at 11:49". That is enough
	 * to know something broke and nothing at all to act on — the line explaining it
	 * is in the container's log, and a Rune replaces the container on every
	 * restart, so it is usually gone before anybody looks. These are the same
	 * failures written somewhere that survives.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';
	import { ago } from '$lib/when.js';

	let errors = $state([]);
	let loaded = $state(false);

	$effect(() => {
		api
			.listErrors()
			.then((r) => {
				errors = r.errors;
				loaded = true;
			})
			.catch((e) => app.toast(humanMessage(e)));
	});

</script>

<section class="panel">
	<header>
		<h2>{t('errors.title')}</h2>
		<p class="hint">
			{t('errors.hint')}
		</p>
	</header>

	{#if errors.length}
		<ul class="rows">
			{#each errors as error (error.id)}
				<li>
					<div class="what">
						<span class="primary-line">
							<span class="status">{error.status}</span>
							{error.what}
						</span>
						<span class="secondary">{error.message}</span>
						<span class="secondary mono">
							{error.method}
							{error.path}{#if error.user_name}{' · '}{error.user_name}{/if}{#if error.request_id}{' · ' +
									error.request_id}{/if}
						</span>
					</div>
					<span class="when">{ago(error.at)}</span>
				</li>
			{/each}
		</ul>
	{:else if loaded}
		<p class="hint">{t('errors.none')}</p>
	{/if}
</section>

<style>
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
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
	}

	.status {
		font-size: var(--text-xs);
		font-weight: 560;
		color: var(--danger);
		border: 1px solid var(--danger);
		border-radius: var(--radius-sm);
		padding: 0 var(--s1);
	}

	.secondary {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		overflow-wrap: anywhere;
	}

	.mono {
		font-family: var(--font-mono);
	}

	.when {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		flex: none;
	}
</style>
