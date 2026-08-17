<script>
	/** Konto: who you are, your password, and the second factor. */
	import { api, humanMessage } from '$lib/api.js';
	import { app, theme, THEMES } from '$lib/stores.svelte.js';

	// --- profile ---------------------------------------------------------------

	let name = $state('');
	let timezone = $state('');
	let locale = $state('da');
	let profileErrors = $state({});
	let profileSaved = $state(false);
	let savingProfile = $state(false);
	let loaded = $state(false);

	// Seeded once. Binding straight to app.user would overwrite what somebody is
	// halfway through typing every time the store reloads.
	$effect(() => {
		if (loaded || !app.user) return;
		name = app.user.name;
		timezone = app.user.timezone;
		locale = app.user.locale;
		loaded = true;
	});

	// Whatever the browser knows, plus the current value in case it is not in the
	// list — a server configured with a zone this browser has never heard of must
	// not have it silently replaced on the next save.
	let zones = $derived.by(() => {
		let all = [];
		try {
			all = Intl.supportedValuesOf('timeZone');
		} catch {
			all = ['Europe/Copenhagen', 'Europe/London', 'UTC'];
		}
		return timezone && !all.includes(timezone) ? [timezone, ...all] : all;
	});

	async function saveProfile(event) {
		event.preventDefault();
		savingProfile = true;
		profileErrors = {};
		profileSaved = false;
		try {
			app.user = await api.updateProfile({ name, timezone, locale });
			profileSaved = true;
		} catch (e) {
			profileErrors = e.fields ?? {};
			if (!Object.keys(profileErrors).length) app.toast(humanMessage(e));
		} finally {
			savingProfile = false;
		}
	}

	// --- password ---------------------------------------------------------------

	let currentPassword = $state('');
	let newPassword = $state('');
	let passwordErrors = $state({});
	let passwordSaved = $state(false);

	async function changePassword(event) {
		event.preventDefault();
		passwordErrors = {};
		passwordSaved = false;
		try {
			await api.changePassword(currentPassword, newPassword);
			currentPassword = '';
			newPassword = '';
			passwordSaved = true;
		} catch (e) {
			passwordErrors = e.fields ?? {};
			if (!Object.keys(passwordErrors).length) app.toast(humanMessage(e));
		}
	}

	// --- two-factor --------------------------------------------------------------

	let totpSecret = $state('');
	let totpURI = $state('');
	let totpCode = $state('');
	let totpErrors = $state({});
	let recoveryCodes = $state([]);
	let remaining = $state(null);
	let disablePassword = $state('');

	$effect(() => {
		if (app.user?.totp_enabled) {
			api.recoveryCodes().then((r) => (remaining = r.remaining)).catch(() => {});
		}
	});

	async function beginTOTP() {
		try {
			const r = await api.totpSetup();
			totpSecret = r.secret;
			totpURI = r.uri;
		} catch (e) {
			app.toast(humanMessage(e));
		}
	}

	async function confirmTOTP(event) {
		event.preventDefault();
		totpErrors = {};
		try {
			const r = await api.totpConfirm(totpCode);
			recoveryCodes = r.recovery_codes;
			totpSecret = '';
			totpURI = '';
			totpCode = '';
			app.user = await api.me();
		} catch (e) {
			totpErrors = e.fields ?? {};
			if (!Object.keys(totpErrors).length) app.toast(humanMessage(e));
		}
	}

	async function disableTOTP(event) {
		event.preventDefault();
		totpErrors = {};
		try {
			await api.totpDisable(disablePassword);
			disablePassword = '';
			recoveryCodes = [];
			remaining = null;
			app.user = await api.me();
		} catch (e) {
			totpErrors = e.fields ?? {};
			if (!Object.keys(totpErrors).length) app.toast(humanMessage(e));
		}
	}

	async function regenerate(event) {
		event.preventDefault();
		totpErrors = {};
		try {
			const r = await api.regenerateRecoveryCodes(disablePassword);
			recoveryCodes = r.recovery_codes;
			remaining = r.recovery_codes.length;
			disablePassword = '';
		} catch (e) {
			totpErrors = e.fields ?? {};
			if (!Object.keys(totpErrors).length) app.toast(humanMessage(e));
		}
	}

	// --- sessions --------------------------------------------------------------

	let sessions = $state([]);

	$effect(() => {
		api.listSessions().then((r) => (sessions = r.sessions)).catch(() => {});
	});

	async function endSession(session) {
		if (session.current) {
			if (!confirm('Det er denne enhed. Du bliver logget ud.')) return;
			await api.endSession(session.id);
			// A full reload rather than clearing the store by hand: the session is
			// gone, so every open socket and every cached answer in this tab is now
			// about an account nobody is signed in to.
			location.href = '/';
			return;
		}

		const previous = sessions;
		sessions = sessions.filter((s) => s.id !== session.id);
		try {
			await api.endSession(session.id);
		} catch (e) {
			sessions = previous;
			app.toast(humanMessage(e));
		}
	}

	/**
	 * "for 2 minutter siden".
	 *
	 * Relative rather than a timestamp, because the question is "was that me, just
	 * now?" and an absolute time makes the reader do the subtraction. Anything
	 * older than a week gets its date instead: "for 23 dage siden" is a number
	 * nobody can place.
	 */
	function ago(iso) {
		const then = new Date(iso);
		const seconds = Math.round((Date.now() - then) / 1000);

		if (seconds < 60) return 'lige nu';
		if (seconds < 3600) return `for ${Math.floor(seconds / 60)} min. siden`;
		if (seconds < 86400) return `for ${Math.floor(seconds / 3600)} timer siden`;
		if (seconds < 604800) return `for ${Math.floor(seconds / 86400)} dage siden`;
		return then.toLocaleDateString('da-DK', { day: 'numeric', month: 'short', year: 'numeric' });
	}
</script>

<section class="panel">
	<header>
		<h2>Profil</h2>
		<p class="hint">
			Tidszonen er ikke pynt: alle datoer i appen bliver afgjort i den, så
			&ldquo;i morgen kl. 9&rdquo; betyder noget andet, når du skifter den.
		</p>
	</header>

	<form onsubmit={saveProfile}>
		<div class="field">
			<label for="name">Navn</label>
			<input
				id="name"
				bind:value={name}
				aria-invalid={profileErrors.name ? 'true' : undefined}
			/>
			{#if profileErrors.name}<p class="error">{profileErrors.name}</p>{/if}
		</div>

		<div class="field">
			<label for="email">E-mail</label>
			<!-- Read-only: the address identifies the account and is what invitations
			     were sent to. Changing it is a re-verification flow, not a field. -->
			<input id="email" value={app.user?.email ?? ''} readonly disabled />
			<p class="hint">Kan ikke ændres her.</p>
		</div>

		<div class="field">
			<label for="timezone">Tidszone</label>
			<select id="timezone" bind:value={timezone}>
				{#each zones as zone (zone)}
					<option value={zone}>{zone}</option>
				{/each}
			</select>
			{#if profileErrors.timezone}<p class="error">{profileErrors.timezone}</p>{/if}
		</div>

		<div class="field">
			<label for="locale">Sprog i hurtig tilføjelse</label>
			<select id="locale" bind:value={locale}>
				<option value="da">Dansk</option>
				<option value="en">English</option>
			</select>
			<p class="hint">
				Afgør hvilken grammatik der tolker en linje — &ldquo;hver mandag&rdquo;
				eller &ldquo;every monday&rdquo;.
			</p>
			{#if profileErrors.locale}<p class="error">{profileErrors.locale}</p>{/if}
		</div>

		<div class="row">
			<button class="primary" type="submit" disabled={savingProfile}>Gem</button>
			{#if profileSaved}<span class="saved">Gemt.</span>{/if}
		</div>
	</form>
</section>

<section class="panel">
	<header>
		<h2>Udseende</h2>
		<p class="hint">
			Gemmes i denne browser. Det er en egenskab ved den skærm, du sidder ved —
			en bærbar i solen og en skærm i et mørkt rum vil have hvert sit svar.
		</p>
	</header>

	<div class="themes">
		{#each THEMES as option (option.id)}
			<button
				class="theme-card"
				class:chosen={theme.current === option.id}
				onclick={() => theme.set(option.id)}
				aria-pressed={theme.current === option.id}
			>
				<!-- data-theme goes on the swatch and not on the card: it redefines
				     --ink too, and a card wearing a dark theme's ink on a light page
				     has a name nobody can read. The swatch shows the colours; the
				     label stays in the page's. -->
				<span class="swatch" data-theme={option.id} aria-hidden="true">
					<span class="swatch-line"></span>
					<span class="swatch-line short"></span>
					<span class="swatch-dot"></span>
				</span>
				<span class="theme-name">{option.name}</span>
				<span class="theme-note">{option.note}</span>
			</button>
		{/each}
	</div>
</section>

<section class="panel">
	<header>
		<h2>Adgangskode</h2>
		<p class="hint">
			At skifte den logger alle andre sessioner ud — hvilket for det meste er
			grunden til at gøre det.
		</p>
	</header>

	<form onsubmit={changePassword}>
		<div class="field">
			<label for="current">Nuværende adgangskode</label>
			<input
				id="current"
				type="password"
				autocomplete="current-password"
				bind:value={currentPassword}
				aria-invalid={passwordErrors.current_password ? 'true' : undefined}
			/>
			{#if passwordErrors.current_password}
				<p class="error">{passwordErrors.current_password}</p>
			{/if}
		</div>

		<div class="field">
			<label for="new">Ny adgangskode</label>
			<input
				id="new"
				type="password"
				autocomplete="new-password"
				bind:value={newPassword}
				aria-invalid={passwordErrors.new_password ? 'true' : undefined}
			/>
			{#if passwordErrors.new_password}<p class="error">{passwordErrors.new_password}</p>{/if}
		</div>

		<div class="row">
			<button class="primary" type="submit">Skift adgangskode</button>
			{#if passwordSaved}<span class="saved">Skiftet.</span>{/if}
		</div>
	</form>
</section>

<section class="panel">
	<header>
		<h2>To-faktor</h2>
		<p class="hint">En engangskode fra en authenticator-app oven i adgangskoden.</p>
	</header>

	{#if recoveryCodes.length}
		<!-- Shown once, and the interface has to say so: only the hashes are kept,
		     so there is no later screen that could show them again. -->
		<div class="field">
			<p class="hint">
				<strong>Skriv dem ned nu.</strong> De vises kun denne ene gang — serveren
				har kun deres hash.
			</p>
			<ul class="codes mono">
				{#each recoveryCodes as code (code)}
					<li>{code}</li>
				{/each}
			</ul>
			<div class="row">
				<button class="secondary" onclick={() => (recoveryCodes = [])}>Jeg har dem</button>
			</div>
		</div>
	{:else if app.user?.totp_enabled}
		<p class="hint">
			Slået til.{#if remaining !== null}
				{remaining} gendannelseskode{remaining === 1 ? '' : 'r'} tilbage.{/if}
		</p>

		<form onsubmit={disableTOTP}>
			<div class="field">
				<label for="disable-pw">Adgangskode</label>
				<input
					id="disable-pw"
					type="password"
					autocomplete="current-password"
					bind:value={disablePassword}
					aria-invalid={totpErrors.password ? 'true' : undefined}
				/>
				{#if totpErrors.password}<p class="error">{totpErrors.password}</p>{/if}
				<p class="hint">
					Kræves til begge handlinger nedenfor. At slå to-faktor fra er præcis
					det, en fremmed med din session ville gøre først.
				</p>
			</div>

			<div class="row">
				<button class="secondary" onclick={regenerate}>Nye gendannelseskoder</button>
				<button class="danger" type="submit">Slå to-faktor fra</button>
			</div>
		</form>
	{:else if totpSecret}
		<div class="field">
			<p class="hint">Scan i din authenticator-app, eller indtast nøglen manuelt.</p>
			<p class="mono secret">{totpSecret}</p>
			<p class="hint mono uri">{totpURI}</p>
		</div>

		<form onsubmit={confirmTOTP}>
			<div class="field">
				<label for="totp">Koden fra appen</label>
				<input
					id="totp"
					bind:value={totpCode}
					inputmode="numeric"
					autocomplete="one-time-code"
					aria-invalid={totpErrors.code ? 'true' : undefined}
				/>
				{#if totpErrors.code}<p class="error">{totpErrors.code}</p>{/if}
				<p class="hint">
					To-faktor er først slået til, når en kode har bevist, at appen faktisk
					har nøglen.
				</p>
			</div>

			<div class="row">
				<button class="primary" type="submit">Bekræft</button>
				<button class="secondary" onclick={() => (totpSecret = '')}>Fortryd</button>
			</div>
		</form>
	{:else}
		<div class="row">
			<button class="primary" onclick={beginTOTP}>Slå to-faktor til</button>
		</div>
	{/if}
</section>

<section class="panel">
	<header>
		<h2>Enheder</h2>
		<p class="hint">
			Hvor du er logget ind. Genkender du ikke en af dem, så log den ud og skift
			adgangskode.
		</p>
	</header>

	<ul class="sessions">
		{#each sessions as session (session.id)}
			<li>
				<div class="what">
					<span class="device">
						{session.device}
						{#if session.current}<span class="badge">denne enhed</span>{/if}
					</span>
					<!-- The address and the time, small: they are what settles "was that
					     me?", and they are not what you read first. -->
					<span class="when" title={session.user_agent}>
						{ago(session.last_seen_at)}{#if session.ip}{' · ' + session.ip}{/if}
					</span>
				</div>
				<button class="secondary" onclick={() => endSession(session)}>Log ud</button>
			</li>
		{/each}
	</ul>
</section>

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
	}

	.sessions {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--s1);
	}

	.sessions li {
		display: flex;
		align-items: center;
		gap: var(--s3);
		padding: var(--s2) 0;
		border-bottom: 1px solid var(--line);
	}

	.sessions li:last-child {
		border-bottom: 0;
	}

	.what {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.device {
		font-size: var(--text-sm);
		display: flex;
		align-items: center;
		gap: var(--s2);
	}

	.badge {
		font-size: var(--text-xs);
		color: var(--accent);
		border: 1px solid var(--accent);
		border-radius: var(--radius-full);
		padding: 0 var(--s2);
	}

	.when {
		font-size: var(--text-xs);
		color: var(--ink-faint);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.codes {
		list-style: none;
		margin: 0;
		padding: var(--s3);
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
		gap: var(--s2);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
	}

	.themes {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
		gap: var(--s3);
	}

	.theme-card {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--s1);
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		text-align: left;
		transition: border-color var(--fast) var(--ease);
	}

	.theme-card:hover {
		border-color: var(--line-strong);
	}

	.theme-card.chosen {
		border-color: var(--accent);
		box-shadow: inset 0 0 0 1px var(--accent);
	}

	/* Painted in the card's own theme rather than the page's — that is the whole
	   point of putting data-theme on the button. */
	.swatch {
		width: 100%;
		height: 54px;
		border-radius: var(--radius-sm);
		background: var(--ground);
		border: 1px solid var(--line);
		padding: var(--s2);
		display: flex;
		flex-direction: column;
		gap: 5px;
		justify-content: center;
		position: relative;
		margin-bottom: var(--s1);
	}

	.swatch-line {
		height: 5px;
		width: 70%;
		border-radius: var(--radius-full);
		background: var(--ink-muted);
	}

	.swatch-line.short {
		width: 45%;
		background: var(--ink-faint);
	}

	.swatch-dot {
		position: absolute;
		right: var(--s2);
		top: var(--s2);
		width: 12px;
		height: 12px;
		border-radius: var(--radius-full);
		background: var(--accent);
	}

	.theme-name {
		font-size: var(--text-sm);
		color: var(--ink);
	}

	.theme-note {
		font-size: var(--text-xs);
		color: var(--ink-faint);
	}

	.secret {
		margin: 0;
		padding: var(--s3);
		background: var(--surface-sunken);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		letter-spacing: 0.08em;
		overflow-wrap: anywhere;
	}

	.uri {
		overflow-wrap: anywhere;
	}
</style>
