<script>
	/**
	 * The approval screen an MCP client sends somebody to: "Claude wants to
	 * connect to your account".
	 *
	 * The server's /oauth/authorize checks the request and hands the browser
	 * here with the query unchanged. Signing in, if it is needed, is the ordinary
	 * sign-in screen — passkeys and second factor included — and this page takes
	 * over once there is somebody to ask.
	 *
	 * Two things are shown, and only one of them can be trusted. The name is
	 * whatever the client called itself when it registered: any program can call
	 * itself Claude. Where the browser goes afterwards is the address the client
	 * registered, and that is what decides who receives the access — so it is
	 * shown, and shown as plainly as the name.
	 */
	import SignIn from '$lib/components/SignIn.svelte';
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';

	const query = typeof location === 'undefined' ? '' : location.search;

	/** @type {{ client_name: string, redirect_to: string } | null} */
	let request = $state(null);
	let problem = $state('');
	let busy = $state(false);

	$effect(() => {
		if (!app.user || request || problem) return;
		api
			.oauthRequest(query)
			.then((r) => (request = r))
			.catch((e) => (problem = e.message || humanMessage(e)));
	});

	async function answer(approve) {
		busy = true;
		try {
			const { redirect_to } = await api.oauthApprove(query, approve);
			// The server only ever answers with an address the client registered,
			// and refuses the schemes a browser would run. Checked again here
			// because this line is what hands it to the browser.
			const scheme = new URL(redirect_to).protocol;
			if (['javascript:', 'data:', 'vbscript:', 'blob:'].includes(scheme)) throw new Error('bad redirect');
			location.assign(redirect_to);
		} catch (e) {
			busy = false;
			app.toast(humanMessage(e));
		}
	}
</script>

<svelte:head><title>{t('connect.title')} · {app.productSlug}</title></svelte:head>

{#if !app.user}
	<SignIn />
{:else}
	<div class="screen">
		<div class="card">
			<div class="brand">
				<span class="rune" aria-hidden="true">ᚹ</span>
				<span class="slug">{app.productSlug}</span>
			</div>

			{#if problem}
				<h1>{t('connect.brokenTitle')}</h1>
				<p class="lede">{t('connect.broken')}</p>
				<p class="detail mono">{problem}</p>
				<a class="link" href="/">{t('connect.toApp')}</a>
			{:else if !request}
				<p class="lede">…</p>
			{:else}
				<h1>{t('connect.heading', { name: request.client_name })}</h1>
				<p class="lede">{t(app.notesOnly ? 'connect.whatNotes' : 'connect.what')}</p>

				<dl>
					<dt>{t('connect.account')}</dt>
					<dd>{app.user.name} · {app.user.email}</dd>
					<dt>{t('connect.sentTo')}</dt>
					<dd class="mono">{request.redirect_to}</dd>
				</dl>

				<p class="hint">{t('connect.revoke')}</p>

				<div class="actions">
					<button class="allow" disabled={busy} onclick={() => answer(true)}>{t('connect.allow')}</button>
					<button class="deny" disabled={busy} onclick={() => answer(false)}>{t('connect.deny')}</button>
				</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.screen {
		min-height: 100dvh;
		display: grid;
		place-items: center;
		padding: var(--s4);
		background: var(--ground);
	}

	.card {
		width: 100%;
		max-width: 380px;
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.brand {
		display: flex;
		align-items: center;
		gap: var(--s3);
		justify-content: center;
	}

	.rune {
		font-size: var(--text-2xl);
		color: var(--accent);
		line-height: 1;
	}

	.slug {
		font-size: var(--text-lg);
		font-weight: 560;
	}

	h1 {
		margin: 0;
		font-size: var(--text-xl);
		font-weight: 560;
		letter-spacing: -0.02em;
		text-align: center;
		text-wrap: balance;
	}

	.lede {
		margin: 0;
		text-align: center;
		color: var(--ink-muted);
		font-size: var(--text-sm);
	}

	dl {
		margin: 0;
		padding: var(--s3) var(--s4);
		display: grid;
		grid-template-columns: auto 1fr;
		gap: var(--s2) var(--s4);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		font-size: var(--text-sm);
	}

	dt {
		color: var(--ink-faint);
	}

	dd {
		margin: 0;
		min-width: 0;
		overflow-wrap: anywhere;
		color: var(--ink);
	}

	.mono {
		font-family: var(--font-mono);
	}

	.hint,
	.detail {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--ink-faint);
		text-align: center;
		overflow-wrap: anywhere;
	}

	.actions {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	.allow {
		padding: var(--s3);
		background: var(--accent);
		color: var(--accent-ink);
		border-radius: var(--radius);
		font-weight: 550;
		transition: background var(--fast) var(--ease);
	}

	.allow:hover:not(:disabled) {
		background: var(--accent-hover);
	}

	.deny {
		padding: var(--s2) var(--s4);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink);
	}

	button:disabled {
		opacity: 0.6;
		cursor: default;
	}

	.link {
		color: var(--ink-faint);
		font-size: var(--text-sm);
		text-align: center;
		text-decoration: none;
	}

	.link:hover {
		color: var(--ink);
	}
</style>
