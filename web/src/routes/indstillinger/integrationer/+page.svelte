<script>
	/** Gmail, the calendar feed, and the mail-to-task address. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { page } from '$app/stores';

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
		connected: { tone: 'ok', text: 'Gmail er forbundet.' },
		state: {
			tone: 'bad',
			text: 'Svaret fra Google passede ikke til det forsøg, der blev startet her. Prøv igen.'
		},
		expired: { tone: 'bad', text: 'Forsøget udløb undervejs. Prøv igen.' },
		invalid: { tone: 'bad', text: 'Svaret fra Google kunne ikke læses. Prøv igen.' },
		failed: { tone: 'bad', text: 'Google afviste ombytningen af koden.' },
		norefresh: {
			tone: 'bad',
			text: 'Google sendte ingen refresh-token. Fjern verdande under din Google-konto og forbind igen.'
		},
		access_denied: { tone: 'bad', text: 'Adgangen blev afvist hos Google.' }
	};

	let callback = $derived.by(() => {
		const value = $page.url.searchParams.get('gmail');
		if (!value) return null;
		return CALLBACK[value] ?? { tone: 'bad', text: `Gmail svarede: ${value}` };
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
		<h2>Gmail</h2>
		<p class="hint">
			Stjernemarkér en mail, eller giv den en etiket, og den bliver en opgave.
			Envejs: at fjerne stjernen gør ikke noget ved opgaven.
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
				Forbind Gmail
			</button>
		</div>

		<!-- The registration behind the button, for whoever can do something about
		     it. Google issues no Gmail access to an unregistered client, and
		     gmail.readonly is a restricted scope — so this step cannot be skipped by
		     any app. It can stop requiring a redeploy, which is what this is. -->
		{#if app.user?.is_admin && client}
			{#if client.from_env}
				<p class="hint">
					Klienten er sat i miljøet (<span class="mono">VERDANDE_GMAIL_CLIENT_ID</span>),
					så den kan ikke ændres her. Det er med vilje: en værdi i runens manifest er
					en beslutning, der bliver anvendt igen ved hver genstart, og en formular,
					der kunne overskrive den, ville gøre manifestet til en løgn.
				</p>
			{:else}
				<details class="setup" open={!client.client_id}>
					<summary>OAuth-klient hos Google</summary>

					<p class="hint">
						Registrér en klient i <span class="mono">Google Cloud Console → APIs &amp;
						Services → Credentials → OAuth client ID → Web application</span>, slå
						Gmail API til, og indsæt de to værdier her. Din egen konto skal stå som
						testbruger, så længe appen ikke er verificeret hos Google.
					</p>

					<div class="field">
						<label for="redirect">Godkendt redirect-URI</label>
						<!-- Read-only and spelled out: it is derived from VERDANDE_BASE_URL,
						     it has to match Google's copy exactly, and the error Google gives
						     when it does not names neither value. Google afviser desuden
						     private IP-adresser over http — det skal være https. -->
						<input id="redirect" readonly value={client.redirect_uri} />
					</div>

					<form onsubmit={saveClient}>
						<div class="field">
							<label for="client-id">Klient-id</label>
							<input id="client-id" bind:value={clientId} autocomplete="off" />
						</div>
						<div class="field">
							<label for="client-secret">Klienthemmelighed</label>
							<input
								id="client-secret"
								type="password"
								bind:value={clientSecret}
								autocomplete="off"
								placeholder={client.has_secret ? 'Gemt — udfyld kun for at skifte den' : ''}
							/>
						</div>
						<div class="row">
							<button class="primary" type="submit" disabled={savingClient}>Gem</button>
							{#if clientSaved}<span class="saved">Gemt.</span>{/if}
						</div>
					</form>
				</details>
			{/if}
		{:else if client && !client.client_id}
			<p class="hint">
				Der er ikke registreret en OAuth-klient hos Google endnu. Det skal en
				administrator gøre, før knappen her kan bruges.
			</p>
		{/if}
	{:else}
		<p class="hint">Forbundet som <strong>{gmail.email || 'ukendt konto'}</strong>.</p>

		<form onsubmit={saveGmail}>
			<div class="field">
				<label for="trigger">Hvad laver en opgave</label>
				<select id="trigger" bind:value={gmail.trigger}>
					<option value="starred">Stjernemarkerede</option>
					<option value="label">En bestemt etiket</option>
					<option value="both">Begge dele</option>
				</select>
			</div>

			{#if gmail.trigger === 'label' || gmail.trigger === 'both'}
				<div class="field">
					<label for="gmail-label">Etikettens navn i Gmail</label>
					<input id="gmail-label" bind:value={gmail.label} placeholder="fx Opgaver" />
				</div>
			{/if}

			<div class="row">
				<button class="primary" type="submit" disabled={savingGmail}>Gem</button>
				{#if gmailSaved}<span class="saved">Gemt.</span>{/if}
				<button class="secondary" onclick={syncNow} disabled={syncing}>Hent nu</button>
				<button class="danger" onclick={disconnectGmail}>Afbryd</button>
			</div>
		</form>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>Kalenderfeed</h2>
		<p class="hint">
			Abonnér i Apple Kalender, Google eller Thunderbird. En kalenderklient kan
			ikke logge ind, så nøglen i adressen <em>er</em> hele adgangskoden — del den
			ikke.
		</p>
	</header>

	<div class="field">
		<label for="feed">Adresse</label>
		<input id="feed" class="mono" value={feedURL} readonly />
	</div>

	<div class="row">
		<button class="secondary" onclick={() => copy(feedURL)}>Kopiér</button>
		<button class="danger" onclick={rotateFeed}>Nyt link</button>
	</div>
	<p class="hint">
		Et nyt link bryder alle eksisterende abonnementer med det samme. Det er
		pointen: det er det, man gør, når det gamle er havnet et forkert sted.
	</p>
</section>

<section class="panel">
	<header>
		<h2>Mail til opgave</h2>
		<p class="hint">
			Send eller videresend en mail hertil, og emnelinjen bliver tolket som en
			hurtig tilføjelse — &ldquo;Fakturer Anders p1 #Firma&rdquo; virker også her.
		</p>
	</header>

	{#if mailConfigured === false}
		<p class="callback" data-tone="bad">
			Der er ingen mailserver sat op på denne server, så adressen herunder kan
			ikke modtage noget endnu. Sæt <span class="mono">VERDANDE_SMTP_HOST</span> og
			de øvrige SMTP-indstillinger, og få mailserveren til at aflevere post til
			<span class="mono">todo+*</span> videre til
			<span class="mono">/inbound/mail</span>.
		</p>
	{/if}

	<div class="field">
		<label for="mail">Din adresse</label>
		<input id="mail" class="mono" value={mailAddress} readonly />
	</div>

	<div class="row">
		<button class="secondary" onclick={() => copy(mailAddress)}>Kopiér</button>
		<button class="danger" onclick={rotateMail}>Ny adresse</button>
	</div>
</section>

<section class="panel">
	<header>
		<h2>CalDAV</h2>
		<p class="hint">
			Tovejs, i modsætning til feedet ovenfor: Apple Påmindelser og Thunderbird
			kan også skrive tilbage.
		</p>
	</header>

	<div class="field">
		<label for="caldav">Server</label>
		<input id="caldav" class="mono" value={`${location.origin}/caldav/`} readonly />
		<p class="hint">
			Brugernavn er din e-mail. Adgangskoden er en
			<a href="/indstillinger/tokens">API-token</a> — ikke din rigtige adgangskode.
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
