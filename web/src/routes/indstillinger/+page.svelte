<script>
	/** Konto: who you are, your password, and the second factor. */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';

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

<style>
	form {
		display: flex;
		flex-direction: column;
		gap: var(--s4);
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
