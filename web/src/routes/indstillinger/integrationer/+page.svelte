<script>
	/** Gmail, Google Calendar, the calendar feed, and the mail-to-task address. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { page } from '$app/stores';
	import { t, plural, tag } from '$lib/i18n.svelte.js';

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
				result?.created
					? plural(result.created, 'int.gmailFetchedOne', 'int.gmailFetchedMany')
					: t('int.gmailNothingNew')
			);
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			syncing = false;
		}
	}

	async function disconnectGmail() {
		if (!confirm(t('int.disconnectQuestion'))) return;
		try {
			await api.disconnectGmail();
			gmail = await api.getGmail();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	// --- Google Calendar -----------------------------------------------------------

	// Its own panel rather than a second mode inside the Gmail one. They sign in
	// through the same registration and are otherwise nothing alike: one turns mail
	// into work you own, the other shows a copy of somebody else's calendar and
	// never writes to it.
	let calendar = $state(null);
	let savingCalendars = $state(false);
	let calendarsSaved = $state(false);
	let syncingCalendar = $state(false);
	/** The ids ticked in the form, which is not what is stored until Save. */
	let shownCalendars = $state([]);

	const CALENDAR_CALLBACK = {
		connected: { tone: 'ok', text: t('int.calendarConnected') },
		state: { tone: 'bad', text: t('int.calendarState') },
		expired: { tone: 'bad', text: t('int.calendarExpired') },
		invalid: { tone: 'bad', text: t('int.calendarInvalid') },
		failed: { tone: 'bad', text: t('int.calendarFailed') },
		norefresh: { tone: 'bad', text: t('int.calendarNoRefresh') },
		access_denied: { tone: 'bad', text: t('int.calendarDenied') },
		// The one worth naming. An OAuth app registered as Internal accepts only
		// accounts inside the organisation, and Google's own screen does not say
		// that in words anybody could act on.
		org_internal: { tone: 'bad', text: t('int.calendarOrgInternal') }
	};

	let calendarCallback = $derived.by(() => {
		const value = $page.url.searchParams.get('calendar');
		if (!value) return null;
		return CALENDAR_CALLBACK[value] ?? { tone: 'bad', text: t('int.calendarOther', { what: value }) };
	});

	$effect(() => {
		loadCalendar();
	});

	async function loadCalendar() {
		try {
			calendar = await api.getCalendar();
			shownCalendars = calendar.calendars.filter((c) => c.shown).map((c) => c.id);
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function connectCalendar() {
		try {
			const { url } = await api.authorizeCalendar();
			location.href = url;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	// --- abonnementer ---------------------------------------------------------
	let subscriptionURL = $state('');
	let subscribing = $state(false);
	let subscriptionError = $state('');

	async function subscribe(event) {
		event.preventDefault();
		const url = subscriptionURL.trim();
		if (!url) return;
		subscribing = true;
		subscriptionError = '';
		try {
			await api.subscribeCalendar(url, '');
			subscriptionURL = '';
			calendar = await api.getCalendar();
		} catch (e) {
			// Feltets egen fejl frem for en toast: den hører til det felt, der lige
			// blev skrevet i, og en toast, der forsvinder, er en fejlmeddelelse, man
			// skal huske.
			subscriptionError = e.fields?.url ?? humanMessage(e);
		} finally {
			subscribing = false;
		}
	}

	async function unsubscribe(sub) {
		if (!confirm(t('int.unsubscribeQuestion', { name: sub.account || sub.url }))) return;
		try {
			await api.unsubscribeCalendar(sub.id);
			calendar = await api.getCalendar();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function saveCalendars(event) {
		event.preventDefault();
		savingCalendars = true;
		calendarsSaved = false;
		try {
			await api.setCalendars(shownCalendars);
			calendarsSaved = true;
			// And fetch, without being asked again.
			//
			// Ticking a calendar is somebody saying "show me this one". Saving the
			// choice and leaving the grid empty until the sweep comes round a quarter
			// of an hour later is a calendar that says a fortnight is clear — and the
			// person is on this page, watching, with the button under their hand.
			// Also what refreshes the panel: syncCalendarNow reloads it when it is done.
			if (shownCalendars.length) {
				await syncCalendarNow();
			} else {
				await loadCalendar();
			}
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			savingCalendars = false;
		}
	}

	async function syncCalendarNow() {
		syncingCalendar = true;
		try {
			const result = await api.syncCalendar();
			app.toast(
				result?.events
					? plural(result.events, 'int.calendarFetchedOne', 'int.calendarFetchedMany')
					: t('int.calendarNothingNew')
			);
			await loadCalendar();
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			syncingCalendar = false;
		}
	}

	async function disconnectCalendar() {
		if (!confirm(t('int.disconnectCalendarQuestion'))) return;
		try {
			await api.disconnectCalendar();
			await loadCalendar();
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	function toggleCalendar(id) {
		shownCalendars = shownCalendars.includes(id)
			? shownCalendars.filter((c) => c !== id)
			: [...shownCalendars, id];
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
		if (!confirm(t('int.newLinkQuestion'))) return;
		try {
			feedURL = (await api.rotateFeed()).url;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function rotateMail() {
		if (!confirm(t('int.newAddressQuestion'))) return;
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
			app.toast(t('int.copied'));
		} catch {
			app.toast(t('int.copyFailed'));
		}
	}

	// --- mailboxes ----------------------------------------------------------------

	let mailboxes = $state([]);
	let addingMailbox = $state(false);
	let connecting = $state(false);
	let syncingId = $state(null);
	let newMailbox = $state({ host: '', username: '', password: '' });

	$effect(() => {
		loadMailboxes();
	});

	async function loadMailboxes() {
		try {
			mailboxes = (await api.mailboxes()).mailboxes ?? [];
		} catch {
			// A settings page that will not render because one panel could not load
			// is worse than a panel that is empty.
			mailboxes = [];
		}
	}

	async function addMailbox(event) {
		event.preventDefault();
		connecting = true;
		try {
			// The server dials before it saves, so an error here is the host's own
			// refusal — a wrong app password, a host that is not listening.
			await api.addMailbox({ ...newMailbox });
			newMailbox = { host: '', username: '', password: '' };
			addingMailbox = false;
			await loadMailboxes();
			app.toast(t('int.mailboxConnected'));
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			connecting = false;
		}
	}

	async function syncMailbox(box) {
		syncingId = box.id;
		try {
			const result = await api.syncMailbox(box.id);
			app.toast(
				result?.created
					? plural(result.created, 'int.gmailFetchedOne', 'int.gmailFetchedMany')
					: t('int.gmailNothingNew')
			);
			await loadMailboxes();
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			syncingId = null;
		}
	}

	async function removeMailbox(box) {
		if (!confirm(t('int.disconnectMailbox', { name: box.name }))) return;
		try {
			await api.deleteMailbox(box.id);
			await loadMailboxes();
		} catch (e) {
			app.toast(humanMessage(e));
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
		     gmail.modify is a restricted scope — so this step cannot be skipped by
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
		<h2>{t('int.calendar')}</h2>
		<p class="hint">{t('int.calendarHint')}</p>
	</header>

	{#if calendarCallback}
		<p class="callback" data-tone={calendarCallback.tone}>{calendarCallback.text}</p>
	{/if}

	{#if calendar === null}
		<p class="empty">…</p>
	{:else if !calendar.connected}
		<div class="row">
			<button class="primary" onclick={connectCalendar} disabled={!calendar.has_client}>
				{t('int.connectCalendar')}
			</button>
		</div>

		<!-- The registration is the Gmail panel's — one client, two features — so
		     this only spells out the second redirect URI, which has to be added to
		     it. It is the single most likely thing to be wrong, and the error Google
		     gives for it names neither value. -->
		{#if app.user?.is_admin}
			<div class="field">
				<label for="calendar-redirect">{t('int.redirectURI')}</label>
				<input id="calendar-redirect" class="mono" readonly value={calendar.redirect_uri} />
				<p class="hint">{t('int.calendarSameClient')}</p>
			</div>
		{:else if !calendar.has_client}
			<p class="hint">{t('int.noClient')}</p>
		{/if}
	{:else}
		<p class="hint">
			{t('int.calendarConnectedAs')}
			<strong>{calendar.account || t('int.unknownAccount')}</strong>.
		</p>

		<form onsubmit={saveCalendars}>
			{#if calendar.calendars.length}
				<fieldset>
					<legend>{t('int.whichCalendars')}</legend>
					{#each calendar.calendars as c (c.id)}
						<label class="pick">
							<input
								type="checkbox"
								checked={shownCalendars.includes(c.id)}
								onchange={() => toggleCalendar(c.id)}
							/>
							<!-- Google's own colour for the calendar, which is what the grid
							     draws its events in. Told apart by the colour the person
							     already knows it by, not by one of verdande's ten names. -->
							<span class="swatch" style:background={c.colour || 'var(--line-strong)'}></span>
							{c.name}
						</label>
					{/each}
				</fieldset>
			{:else}
				<p class="hint">{t('int.noCalendars')}</p>
			{/if}

			<p class="hint">
				{calendar.last_sync_at
					? t('int.calendarLastSync', {
							when: new Date(calendar.last_sync_at).toLocaleString(tag())
						})
					: t('int.calendarNeverSynced')}
			</p>

			<div class="row">
				<button class="primary" type="submit" disabled={savingCalendars}>{t('int.save')}</button>
				{#if calendarsSaved}<span class="saved">{t('int.saved')}</span>{/if}
				<button class="secondary" onclick={syncCalendarNow} disabled={syncingCalendar}>
					{syncingCalendar ? t('int.fetching') : t('int.fetchNow')}
				</button>
				<button class="danger" onclick={disconnectCalendar}>{t('int.disconnect')}</button>
			</div>
		</form>
	{/if}

	<!-- Abonnementer står uden for Google-forgreningen ovenfor, og det er hele
	     pointen: en konto, der ikke kan bruge instansens OAuth-klient — en privat
	     Gmail mod en Internal-registrering — kan stadig abonnere på en adresse. -->
	{#if calendar !== null}
		<div class="subs">
			<h3>{t('int.subscriptions')}</h3>
			<p class="hint">{t('int.subscriptionsHint')}</p>

			{#if calendar.subscriptions?.length}
				<ul class="subs-list">
					{#each calendar.subscriptions as sub (sub.id)}
						<li>
							<span class="name">{sub.account || sub.url}</span>
							<span class="url mono">{sub.url}</span>
							<button class="danger" onclick={() => unsubscribe(sub)}>
								{t('int.unsubscribe')}
							</button>
						</li>
					{/each}
				</ul>
			{/if}

			<form onsubmit={subscribe}>
				<div class="field">
					<label for="calendar-url">{t('int.subscriptionURL')}</label>
					<input
						id="calendar-url"
						class="mono"
						bind:value={subscriptionURL}
						placeholder="https://… eller webcal://…"
					/>
					{#if subscriptionError}<p class="error">{subscriptionError}</p>{/if}
				</div>
				<div class="row">
					<button class="primary" type="submit" disabled={subscribing}>
						{subscribing ? t('int.subscribing') : t('int.subscribe')}
					</button>
				</div>
			</form>
		</div>
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

<!-- Mailboxes read over IMAP: iCloud, Fastmail, any ordinary host. Its own panel
     rather than a second mode inside the Gmail one, because they are not the same
     thing wearing different clothes — one is an app you sign in through, the other
     is a password you hold. -->
<section class="panel">
	<header>
		<h2>{t('int.mailboxes')}</h2>
		<p class="hint">{t('int.mailboxesHint')}</p>
	</header>

	{#if mailboxes.length}
		<ul class="mailboxes">
			{#each mailboxes as box (box.id)}
				<li>
					<div class="what">
						<strong>{box.name}</strong>
						<span class="hint">{box.username} · {box.host}</span>
					</div>
					<button class="ghost" onclick={() => syncMailbox(box)} disabled={syncingId === box.id}>
						{syncingId === box.id ? t('int.fetching') : t('int.fetchNow')}
					</button>
					<button class="ghost remove" onclick={() => removeMailbox(box)}>
						{t('int.disconnect')}
					</button>
				</li>
			{/each}
		</ul>
	{/if}

	{#if addingMailbox}
		<form class="add-mailbox" onsubmit={addMailbox}>
			<div class="field">
				<label for="mb-host">{t('int.host')}</label>
				<input id="mb-host" bind:value={newMailbox.host} placeholder="imap.mail.me.com:993" />
			</div>
			<div class="field">
				<label for="mb-user">{t('int.username')}</label>
				<input id="mb-user" bind:value={newMailbox.username} autocomplete="off" />
			</div>
			<div class="field">
				<label for="mb-pass">{t('int.appPassword')}</label>
				<input id="mb-pass" type="password" bind:value={newMailbox.password} autocomplete="off" />
				<p class="hint">{t('int.appPasswordHint')}</p>
			</div>
			<div class="row">
				<button type="submit" class="primary" disabled={connecting}>
					{connecting ? t('int.connecting') : t('int.connect')}
				</button>
				<button type="button" class="ghost" onclick={() => (addingMailbox = false)}>
					{t('account.cancel')}
				</button>
			</div>
		</form>
	{:else}
		<div class="row">
			<button class="ghost" onclick={() => (addingMailbox = true)}>{t('int.addMailbox')}</button>
		</div>
	{/if}
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

	fieldset {
		border: 1px solid var(--line);
		border-radius: var(--radius);
		padding: var(--s3);
		display: flex;
		flex-direction: column;
		gap: var(--s2);
	}

	legend {
		font-size: var(--text-sm);
		color: var(--ink-muted);
		padding: 0 var(--s1);
	}

	.pick {
		display: flex;
		align-items: center;
		gap: var(--s2);
		font-size: var(--text-sm);
	}

	/* A solid mark beside a label that carries the meaning, so it only has to be
	   tellable from its neighbours and visible on both near-black and white — the
	   same job the project colours' dots do. */
	.swatch {
		width: 10px;
		height: 10px;
		border-radius: var(--radius-full);
		flex: none;
	}

	a {
		color: var(--accent);
	}
</style>
