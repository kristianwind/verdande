<script>
	/**
	 * Personal API tokens.
	 *
	 * The one screen in the app that shows a secret. It is shown exactly once, and
	 * the copy has to say so — the server keeps a hash, so there is no later screen
	 * that could reveal it even if this one offered to.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';

	let tokens = $state([]);
	let loading = $state(true);

	let name = $state('');
	let expiresInDays = $state(0);
	let errors = $state({});
	let creating = $state(false);

	/** The plaintext of the token just minted. Held only in this variable. */
	let fresh = $state(null);
	let copied = $state(false);
	let connectorCopied = $state(false);

	/** What Claude's connector dialog needs, which is a URL and nothing else. */
	let connectorURL = $derived(fresh ? `${location.origin}/mcp?key=${fresh.token}` : '');

	$effect(() => {
		api
			.listTokens()
			.then((r) => (tokens = r.tokens))
			.catch((e) => app.toast(humanMessage(e)))
			.finally(() => (loading = false));
	});

	async function create(event) {
		event.preventDefault();
		errors = {};
		creating = true;
		try {
			const created = await api.createToken(name.trim(), Number(expiresInDays));
			fresh = created;
			copied = false;
			name = '';
			expiresInDays = 0;
			tokens = [{ ...created, token: undefined }, ...tokens];
		} catch (e) {
			errors = e.fields ?? {};
			if (!Object.keys(errors).length) app.toast(humanMessage(e));
		} finally {
			creating = false;
		}
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(fresh.token);
			copied = true;
		} catch {
			// Insecure context, or permission refused. The token is on screen and
			// selectable, so this is a convenience that failed, not the feature.
			app.toast('Kunne ikke kopiere — markér teksten i stedet.');
		}
	}

	async function copyConnector() {
		try {
			await navigator.clipboard.writeText(connectorURL);
			connectorCopied = true;
		} catch {
			app.toast('Kunne ikke kopiere — markér teksten i stedet.');
		}
	}

	async function revoke(token) {
		if (!confirm(`Tilbagekald "${token.name}"? Alt, der bruger den, holder op med at virke.`))
			return;

		const previous = tokens;
		tokens = tokens.filter((t) => t.id !== token.id);
		try {
			await api.deleteToken(token.id);
		} catch (e) {
			tokens = previous;
			app.toast(humanMessage(e));
		}
	}

	const date = (iso) =>
		iso ? new Date(iso).toLocaleDateString('da-DK', { day: 'numeric', month: 'short', year: 'numeric' }) : null;

	const expired = (iso) => iso && new Date(iso) < new Date();
</script>

<section class="panel">
	<header>
		<h2>{t('tokens.title')}</h2>
		<p class="hint">
			{t('tokens.hint')}
		</p>
	</header>

	{#if fresh}
		<div class="fresh">
			<p class="hint">
				<strong>{t('tokens.copyNow')}</strong>
				{t('tokens.onceOnly')}
			</p>
			<p class="mono value">{fresh.token}</p>
			<div class="row">
				<button class="primary" onclick={copy}>{t('tokens.copy')}</button>
				{#if copied}<span class="saved">{t('tokens.copied')}</span>{/if}
				<button class="secondary" onclick={() => (fresh = null)}>{t('tokens.done')}</button>
			</div>

			<!-- Shown here and nowhere else, because it contains the token, and the
			     token exists only in this moment. Claude's connector dialog takes a
			     URL and nothing else — there is no field for a header — so this is
			     the form it has to be in. -->
			<div class="also">
				<p class="hint">
					{t('tokens.mcpBefore')} <a href="/indstillinger/integrationer">{t('tokens.mcpLink')}</a>
					{t('tokens.mcpAfter')}
				</p>
				<p class="mono value">{connectorURL}</p>
				<div class="row">
					<button class="secondary" onclick={copyConnector}>{t('tokens.copyAddress')}</button>
					{#if connectorCopied}<span class="saved">{t('tokens.copied')}</span>{/if}
				</div>
			</div>
		</div>
	{/if}

	<form onsubmit={create}>
		<div class="field">
			<label for="token-name">{t('tokens.name')}</label>
			<input
				id="token-name"
				bind:value={name}
				placeholder={t('tokens.namePlaceholder')}
				aria-invalid={errors.name ? 'true' : undefined}
			/>
			{#if errors.name}<p class="error">{errors.name}</p>{/if}
		</div>

		<div class="field">
			<label for="token-expiry">{t('tokens.expires')}</label>
			<select id="token-expiry" bind:value={expiresInDays}>
				<option value={0}>{t('tokens.never')}</option>
				<option value={30}>{t('tokens.in30')}</option>
				<option value={90}>{t('tokens.in90')}</option>
				<option value={365}>{t('tokens.inYear')}</option>
			</select>
			<p class="hint">
				{t('tokens.expiryHint')}
			</p>
			{#if errors.expires_in_days}<p class="error">{errors.expires_in_days}</p>{/if}
		</div>

		<div class="row">
			<button class="primary" type="submit" disabled={creating}>{t('tokens.create')}</button>
		</div>
	</form>

	{#if loading}
		<p class="empty">…</p>
	{:else if tokens.length === 0}
		<p class="empty">{t('tokens.none')}</p>
	{:else}
		<ul class="list">
			{#each tokens as token (token.id)}
				<li>
					<div class="what">
						<span class="token-name">{token.name}</span>
						<span class="meta mono">
							{token.prefix}…
							{#if token.last_used_at}
								· {t('tokens.lastUsed', { when: date(token.last_used_at) })}
							{:else}
								· {t('tokens.neverUsed')}
							{/if}
							{#if token.expires_at}
								· {expired(token.expires_at)
									? t('tokens.expired', { when: date(token.expires_at) })
									: t('tokens.expiresOn', { when: date(token.expires_at) })}
							{/if}
						</span>
					</div>
					<button class="revoke" onclick={() => revoke(token)} aria-label={t('tokens.revokeNamed', { name: token.name })}>
						{t('tokens.revoke')}
					</button>
				</li>
			{/each}
		</ul>
	{/if}
</section>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.fresh {
		display: flex;
		flex-direction: column;
		gap: var(--s3);
		padding: var(--s4);
		background: var(--accent-sunken);
		border: 1px solid var(--accent);
		border-radius: var(--radius);
	}

	.value {
		margin: 0;
		padding: var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		overflow-wrap: anywhere;
		user-select: all;
	}

	.also {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		padding-top: var(--s3);
		border-top: 1px solid var(--accent);
	}

	.also a {
		color: inherit;
	}

	.what {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
		flex: 1;
	}

	.token-name {
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}

	.meta {
		color: var(--ink-faint);
	}

	.revoke {
		flex: none;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		padding: var(--s1) var(--s2);
		border-radius: var(--radius-sm);
		transition: color var(--fast) var(--ease);
	}

	.revoke:hover {
		color: var(--danger);
		background: var(--danger-sunken);
	}
</style>
