<script>
	/** Gmail, the calendar feed, and the mail-to-task address. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { page } from '$app/stores';
	import { t } from '$lib/i18n.svelte.js';

	// --- Gmail ------------------------------------------------------------------

	let gmail = $state(null);
	let savingGmail = $state(false);
	let gmailSaved = $state(false);
	let syncing = $state(false);

	// The OAuth client is the instance's registration with Google, not anybody's
	// mailbox — so it is read only for administrators, and only when they are the
	// ones who could act on it.
	let client = $state(null);
	let clientId = $state('');
	let clientSecret = $state('');
	let savingClient = $state(false);
	let clientSaved = $state(false);

	$effect(() => {
		if (!app.user?.is_admin) return;
		api
			.gmailClient()
			.then((c) => {
				client = c;
				clientId = c.client_id ?? '';
			})
			.catch(() => {
				// Not worth a toast: the panel simply does not offer the fields, which
				// is the same thing it does for everybody who is not an administrator.
			});
	});

	async function saveClient(event) {
		event.preventDefault();
		savingClient = true;
		clientSaved = false;
		try {
			await api.setGmailClient({ client_id: clientId, client_secret: clientSecret });
			// Cleared rather than kept: the field is a password field and the value is
			// now stored. Leaving it filled is how a secret ends up in a screenshot.
			clientSecret = '';
			client = await api.gmailClient();
			clientSaved = true;
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			savingClient = false;
		}
	}

	/**
	 * What the OAuth callback redirected back with. It arrives as a query parameter
	 * because the callback is a top-level navigation from Google — there is no fetch
	 * whose response could have carried it.
	 */
	const CALLBACK = {
		connected: { tone: 'ok', text: t('int.gmailConnected') },
		state: {
			tone: 'bad',
			text: t('int.gmailState')
		},
		expired: { tone: 'bad', text: t('int.gmailExpired') },
		invalid: { tone: 'bad', text: t('int.gmailInvalid') },
		failed: { tone: 'bad', text: t('int.gmailFailed') },
		norefresh: {
			tone: 'bad',
			text: t('int.gmailNoRefresh')
		},
		access_denied: { tone: 'bad', text: t('int.gmailDenied') }
	};

	let callback = $derived.by(() => {
		const value = $page.url.searchParams.get('gmail');
		if (!value) return null;
		return CALLBACK[value] ?? { tone: 'bad', text: t('int.gmailOther', { what: value }) };
	});

	$effect(() => {
		api
			.getGmail()
			.then((g) => (gmail = g))
			.catch((e) => app.toast(humanMessage(e)));
	});

	async function connect() {
		try {
			const { url } = await api.authorizeGmail();
			location.href = url;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function saveGmail(event) {
		event.preventDefault();
		savingGmail = true;
		gmailSaved = false;
		try {
			await api.setGmail({ trigger: gmail.trigger, label: gmail.label ?? '' });
			gmailSaved = true;
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			savingGmail = false;
		}
	}

	async function syncNow() {
		syncing = true;
		try {
			const result = await api.syncGmail();
			app.toast(
				result?.created ? `${result.created} opgave(r) hentet.` : 'Ingen nye beskeder.'
			);
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			syncing = false;
		}
	}

	async function disconnectGmail() {
		if (!confirm('Afbryd forbindelsen til Gmail? Tokens bliver glemt.')) return;
		try {
			await api.disconnectGmail();
			gmail = await api.getGmail();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	// --- calendar feed -------------------------------------------------------------

	let feedURL = $state('');
	let mailAddress = $state('');
	/** Null until loaded, so the warning does not flash before the answer is in. */
	let mailConfigured = $state(null);

	$effect(() => {
		api.feed().then((r) => (feedURL = r.url)).catch(() => {});
		api
			.getMailAddress()
			.then((r) => {
				mailAddress = r.address;
				mailConfigured = r.configured;
			})
			.catch(() => {});
	});

	async function rotateFeed() {
		if (!confirm('Nyt feed-link? Alle eksisterende abonnementer holder op med at virke.')) return;
		try {
			feedURL = (await api.rotateFeed()).url;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function rotateMail() {
		if (!confirm('Ny adresse? Mail til den gamle bliver afvist.')) return;
		try {
			const r = await api.rotateMailAddress();
			mailAddress = r.address;
			mailConfigured = r.configured;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function copy(text) {
		try {
			await navigator.clipboard.writeText(text);
			app.toast('Kopieret.');
		} catch {
			app.toast('Kunne ikke kopiere — markér teksten i stedet.');
		}
	}
</script>

<section class="panel">
	<header>
		<h2>{t('int.gmail')}</h2>
		<p class="hint">
			{t('int.gmailHint')}
		</p>
	</header>

	{#if callback}
		<p class="callback" data-tone={callback.tone}>{callback.text}</p>
	{/if}

	{#if gmail === null}
		<p class="empty">…</p>
	{:else if !gmail.connected}
		<div class="row">
			<button class="primary" onclick={connect} disabled={client && !client.client_id}>
				{t('int.connectGmail')}
			</button>
		</div>

		<!-- The registration behind the button, for whoever can do something about
		     it. Google issues no Gmail access to an unregistered client, and
		     gmail.readonly is a restricted scope — so this step cannot be skipped by
		     any app. It can stop requiring a redeploy, which is what this is. -->
		{#if app.user?.is_admin && client}
			{#if client.from_env}
				<p class="hint">
					{t('int.fromEnv')}
				</p>
			{:else}
				<details class="setup" open={!client.client_id}>
					<summary>{t('int.oauthClient')}</summary>

					<p class="hint">
						{t('int.registerHint')}
					</p>

					<div class="field">
						<label for="redirect">{t('int.redirectURI')}</label>
						<!-- Read-only and spelled out: it is derived from VERDANDE_BASE_URL,
						     it has to match Google's copy exactly, and the error Google gives
						     when it does not names neither value. Google afviser desuden
						     private IP-adresser over http — det skal være https. -->
						<input id="redirect" readonly value={client.redirect_uri} />
					</div>

					<form onsubmit={saveClient}>
						<div class="field">
							<label for="client-id">{t('int.clientID')}</label>
							<input id="client-id" bind:value={clientId} autocomplete="off" />
						</div>
						<div class="field">
							<label for="client-secret">{t('int.clientSecret')}</label>
							<input
								id="client-secret"
								type="password"
								bind:value={clientSecret}
								autocomplete="off"
								placeholder={client.has_secret ? t('int.secretStored') : ''}
							/>
						</div>
						<div class="row">
							<button class="primary" type="submit" disabled={savingClient}>{t('int.save')}</button>
							{#if clientSaved}<span class="saved">{t('int.saved')}</span>{/if}
						</div>
					</form>
				</details>
			{/if}
		{:else if client && !client.client_id}
			<p class="hint">
				{t('int.noClient')}
			</p>
		{/if}
	{:else}
		<p class="hint">{t('int.connectedAs')} <strong>{gmail.email || t('int.unknownAccount')}</strong>.</p>

		<form onsubmit={saveGmail}>
			<div class="field">
				<label for="trigger">{t('int.whatMakesATask')}</label>
				<select id="trigger" bind:value={gmail.trigger}>
					<option value="starred">{t('int.starred')}</option>
					<option value="label">{t('int.aLabel')}</option>
					<option value="both">{t('int.both')}</option>
				</select>
			</div>

			{#if gmail.trigger === 'label' || gmail.trigger === 'both'}
				<div class="field">
					<label for="gmail-label">{t('int.gmailLabelName')}</label>
					<input id="gmail-label" bind:value={gmail.label} placeholder={t('int.gmailLabelExample')} />
				</div>
			{/if}

			<div class="row">
				<button class="primary" type="submit" disabled={savingGmail}>{t('int.save')}</button>
				{#if gmailSaved}<span class="saved">{t('int.saved')}</span>{/if}
				<button class="secondary" onclick={syncNow} disabled={syncing}>{t('int.fetchNow')}</button>
				<button class="danger" onclick={disconnectGmail}>{t('int.disconnect')}</button>
			</div>
		</form>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>{t('int.feed')}</h2>
		<p class="hint">
			{t('int.feedHint')}
		</p>
	</header>

	<div class="field">
		<label for="feed">{t('int.address')}</label>
		<input id="feed" class="mono" value={feedURL} readonly />
	</div>

	<div class="row">
		<button class="secondary" onclick={() => copy(feedURL)}>{t('int.copy')}</button>
		<button class="danger" onclick={rotateFeed}>{t('int.newLink')}</button>
	</div>
	<p class="hint">
		{t('int.newLinkHint')}
	</p>
</section>

<section class="panel">
	<header>
		<h2>{t('int.mailToTask')}</h2>
		<p class="hint">
			{t('int.mailHint')}
		</p>
	</header>

	{#if mailConfigured === false}
		<p class="callback" data-tone="bad">
			{t('int.noMailServer')}
		</p>
	{/if}

	<div class="field">
		<label for="mail">{t('int.yourAddress')}</label>
		<input id="mail" class="mono" value={mailAddress} readonly />
	</div>

	<div class="row">
		<button class="secondary" onclick={() => copy(mailAddress)}>{t('int.copy')}</button>
		<button class="danger" onclick={rotateMail}>{t('int.newAddress')}</button>
	</div>
</section>

<section class="panel">
	<header>
		<h2>{t('int.caldav')}</h2>
		<p class="hint">
			{t('int.caldavHint')}
		</p>
	</header>

	<div class="field">
		<label for="caldav">{t('int.server')}</label>
		<input id="caldav" class="mono" value={`${location.origin}/caldav/`} readonly />
		<p class="hint">
			{t('int.caldavAuth')}
		</p>
	</div>
</section>

<style>
	/* Folded away once it is set: it is a one-off registration, and a panel that
	   keeps showing its own setup instructions after setup reads as unfinished. */
	.setup {
		border: 1px solid var(--line);
		border-radius: var(--radius);
		padding: var(--s3);
		display: flex;
		flex-direction: column;
		gap: var(--s3);
	}

	.setup summary {
		cursor: pointer;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	.setup input[readonly] {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}

	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.callback {
		margin: 0;
		padding: var(--s3);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		border: 1px solid;
	}

	.callback[data-tone='ok'] {
		color: var(--accent);
		background: var(--accent-sunken);
		border-color: var(--accent);
	}

	.callback[data-tone='bad'] {
		color: var(--danger);
		background: var(--danger-sunken);
		border-color: var(--danger);
	}

	a {
		color: var(--accent);
	}
</style>
