<script>
	/** Konto: who you are, your password, and the second factor. */
	import { api, humanMessage } from '$lib/api.js';
	import { app, theme, THEMES, look, LOOKS, menuSize, textSize, SIZES } from '$lib/stores.svelte.js';
	import { t, plural, i18n } from '$lib/i18n.svelte.js';
	import { supported, register } from '$lib/passkey.js';
	import { ago } from '$lib/when.js';

	// --- profile ---------------------------------------------------------------

	let name = $state('');
	let timezone = $state('');
	let locale = $state('da');

	// --- passkeys -------------------------------------------------------------------

	let passkeys = $state([]);
	let passkeyName = $state('');
	let passkeyError = $state('');
	let registering = $state(false);
	let passkeySupported = $state(true);

	$effect(() => {
		passkeySupported = supported();
		if (!passkeySupported) return;
		api
			.listPasskeys()
			.then((r) => {
				passkeys = r.passkeys;
				// The server has the other half of the answer: this browser can do
				// passkeys, and this deployment may still be on an address no
				// authenticator will accept.
				passkeySupported = r.available;
			})
			.catch(() => {});
	});

	async function addPasskey(event) {
		event.preventDefault();
		passkeyError = '';
		registering = true;
		try {
			passkeys = [...passkeys, await register(passkeyName.trim())];
			passkeyName = '';
		} catch (e) {
			// A cancelled prompt is not a failure — the person changed their mind,
			// and an error message for that reads as though something broke.
			if (e?.name !== 'NotAllowedError' && e?.message !== 'cancelled') {
				passkeyError = humanMessage(e);
			}
		} finally {
			registering = false;
		}
	}

	async function removePasskey(key) {
		if (!confirm(t('passkey.removeQuestion', { name: key.name }))) return;
		const previous = passkeys;
		passkeys = passkeys.filter((k) => k.id !== key.id);
		try {
			await api.deletePasskey(key.id);
		} catch (e) {
			passkeys = previous;
			app.toast(humanMessage(e));
		}
	}
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
			// Applied straight away rather than on the next load. `i18n.locale` is
			// $state, so every component that calls t() redraws — which is the whole
			// reason the module is .svelte.js.
			i18n.set(app.user.locale);
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
	let totpQR = $state('');
	let totpCode = $state('');
	let totpErrors = $state({});
	let recoveryCodes = $state([]);
	let remaining = $state(null);
	let disablePassword = $state('');
	let enablePassword = $state('');

	$effect(() => {
		if (app.user?.totp_enabled) {
			api.recoveryCodes().then((r) => (remaining = r.remaining)).catch(() => {});
		}
	});

	async function beginTOTP(event) {
		event?.preventDefault?.();
		totpErrors = {};
		try {
			// Adgangskoden, ligesom når man slår den fra. At slå den til er mindst
			// lige så indgribende som at slå den fra — det afgør, hvilken enhed der
			// fra nu af må sige ja — og en dør, der åbnes lettere end den lukkes, er
			// ikke en dør.
			const r = await api.totpSetup(enablePassword);
			enablePassword = '';
			totpSecret = r.secret;
			totpURI = r.uri;
			totpQR = r.qr;
		} catch (e) {
			totpErrors = e.fields ?? {};
			if (!Object.keys(totpErrors).length) app.toast(humanMessage(e));
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
			totpQR = '';
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
			if (!confirm(t('account.signOutThisDevice'))) return;
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
</script>

<section class="panel">
	<header>
		<h2>{t('account.profile')}</h2>
		<p class="hint">
			{t('account.profileHint')}
		</p>
	</header>

	<form onsubmit={saveProfile}>
		<div class="field">
			<label for="name">{t('account.name')}</label>
			<input
				id="name"
				bind:value={name}
				aria-invalid={profileErrors.name ? 'true' : undefined}
			/>
			{#if profileErrors.name}<p class="error">{profileErrors.name}</p>{/if}
		</div>

		<div class="field">
			<label for="email">{t('account.email')}</label>
			<!-- Read-only: the address identifies the account and is what invitations
			     were sent to. Changing it is a re-verification flow, not a field. -->
			<input id="email" value={app.user?.email ?? ''} readonly disabled />
			<p class="hint">{t('account.emailFixed')}</p>
		</div>

		<div class="field">
			<label for="timezone">{t('account.timezone')}</label>
			<select id="timezone" bind:value={timezone}>
				{#each zones as zone (zone)}
					<option value={zone}>{zone}</option>
				{/each}
			</select>
			{#if profileErrors.timezone}<p class="error">{profileErrors.timezone}</p>{/if}
		</div>

		<div class="field">
			<label for="locale">{t('account.parseLanguage')}</label>
			<select id="locale" bind:value={locale}>
				<option value="da">{t('account.danish')}</option>
				<option value="en">{t('account.english')}</option>
			</select>
			<p class="hint">
				{t('account.parseHint')}
			</p>
			{#if profileErrors.locale}<p class="error">{profileErrors.locale}</p>{/if}
		</div>

		<div class="row">
			<button class="primary" type="submit" disabled={savingProfile}>{t('account.save')}</button>
			{#if profileSaved}<span class="saved">{t('account.saved')}</span>{/if}
		</div>
	</form>
</section>

<section class="panel">
	<header>
		<h2>{t('account.appearance')}</h2>
		<p class="hint">
			{t('account.appearanceHint')}
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
				<span class="theme-name">{t(option.name)}</span>
				<span class="theme-note">{t(option.note)}</span>
			</button>
		{/each}
	</div>

	<!-- The second axis. A theme says how bright, a look says how it reads — and
	     they are separate settings because they are separate questions. -->
	<div class="looks">
		<h3>{t('account.look')}</h3>
		<p class="hint">{t('account.lookHint')}</p>
		<div class="look-row">
			{#each LOOKS as option (option.id)}
				<button
					class="look-card"
					class:chosen={look.current === option.id}
					onclick={() => look.set(option.id)}
					aria-pressed={look.current === option.id}
				>
					<!-- Each card is written in the face it selects, because that is the
					     only part of the choice a name cannot carry. -->
					<span class="look-sample" data-look={option.id} aria-hidden="true">Aa</span>
					<span class="theme-name">{t(option.name)}</span>
					<span class="theme-note">{t(option.note)}</span>
				</button>
			{/each}
		</div>
	</div>

	<!-- Two more axes: how large the menu reads and how large the body text reads.
	     Separate on purpose — one is a glance down a list of names, the other is a
	     paragraph you sit and read. -->
	<div class="sizes">
		<h3>{t('account.sizes')}</h3>
		<p class="hint">{t('account.sizesHint')}</p>
		<div class="size-row">
			<div class="size-field">
				<label for="menu-size">{t('account.menuSize')}</label>
				<select
					id="menu-size"
					value={menuSize.current}
					onchange={(e) => menuSize.set(e.currentTarget.value)}
				>
					{#each SIZES as option (option.id)}
						<option value={option.id}>{t(option.name)}</option>
					{/each}
				</select>
			</div>
			<div class="size-field">
				<label for="text-size">{t('account.textSize')}</label>
				<select
					id="text-size"
					value={textSize.current}
					onchange={(e) => textSize.set(e.currentTarget.value)}
				>
					{#each SIZES as option (option.id)}
						<option value={option.id}>{t(option.name)}</option>
					{/each}
				</select>
			</div>
		</div>
	</div>
</section>

<section class="panel">
	<header>
		<h2>{t('account.password')}</h2>
		<p class="hint">
			{t('account.passwordHint')}
		</p>
	</header>

	<form onsubmit={changePassword}>
		<div class="field">
			<label for="current">{t('account.currentPassword')}</label>
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
			<label for="new">{t('account.newPassword')}</label>
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
			<button class="primary" type="submit">{t('account.changePassword')}</button>
			{#if passwordSaved}<span class="saved">{t('account.passwordChanged')}</span>{/if}
		</div>
	</form>
</section>

<section class="panel">
	<header>
		<h2>{t('account.totp')}</h2>
		<p class="hint">{t('account.totpHint')}</p>
	</header>

	{#if recoveryCodes.length}
		<!-- Shown once, and the interface has to say so: only the hashes are kept,
		     so there is no later screen that could show them again. -->
		<div class="field">
			<p class="hint">
				<strong>{t('account.writeThemDown')}</strong> {t('account.recoveryHint')}
			</p>
			<ul class="codes mono">
				{#each recoveryCodes as code (code)}
					<li>{code}</li>
				{/each}
			</ul>
			<div class="row">
				<button class="secondary" onclick={() => (recoveryCodes = [])}>{t('account.gotThem')}</button>
			</div>
		</div>
	{:else if app.user?.totp_enabled}
		<p class="hint">
			{t('account.totpEnabled')}{#if remaining !== null}
				{plural(remaining, 'account.recoveryLeftOne', 'account.recoveryLeftMany')}{/if}
		</p>

		<form onsubmit={disableTOTP}>
			<div class="field">
				<label for="disable-pw">{t('account.password')}</label>
				<input
					id="disable-pw"
					type="password"
					autocomplete="current-password"
					bind:value={disablePassword}
					aria-invalid={totpErrors.password ? 'true' : undefined}
				/>
				{#if totpErrors.password}<p class="error">{totpErrors.password}</p>{/if}
				<p class="hint">
					{t('account.passwordRequired')}
				</p>
			</div>

			<div class="row">
				<button class="secondary" onclick={regenerate}>{t('account.newRecoveryCodes')}</button>
				<button class="danger" type="submit">{t('account.totpOff')}</button>
			</div>
		</form>
	{:else if totpSecret}
		<div class="field">
			<p class="hint">{t('account.scanHint')}</p>
			{#if totpQR}
				<!-- The server renders the otpauth URI as an SVG QR — one dark path on a
				     white square, so it scans in either theme. It is the URI it already
				     sent, drawn; nothing here builds it. -->
				<div class="qr">{@html totpQR}</div>
			{/if}
			<p class="mono secret">{totpSecret}</p>
		</div>

		<form onsubmit={confirmTOTP}>
			<div class="field">
				<label for="totp">{t('account.codeFromApp')}</label>
				<input
					id="totp"
					bind:value={totpCode}
					inputmode="numeric"
					autocomplete="one-time-code"
					aria-invalid={totpErrors.code ? 'true' : undefined}
				/>
				{#if totpErrors.code}<p class="error">{totpErrors.code}</p>{/if}
				<p class="hint">
					{t('account.totpProofHint')}
				</p>
			</div>

			<div class="row">
				<button class="primary" type="submit">{t('account.confirm')}</button>
				<button
				class="secondary"
				onclick={() => {
					totpSecret = '';
					totpURI = '';
					totpQR = '';
				}}>{t('account.cancel')}</button
			>
			</div>
		</form>
	{:else}
		<form onsubmit={beginTOTP}>
			<div class="field">
				<label for="enable-pw">{t('account.password')}</label>
				<input
					id="enable-pw"
					type="password"
					autocomplete="current-password"
					bind:value={enablePassword}
				/>
				{#if totpErrors.password}<p class="error">{totpErrors.password}</p>{/if}
			</div>
			<div class="row">
				<button class="primary" type="submit">{t('account.totpOn')}</button>
			</div>
		</form>
	{/if}
</section>

<!-- Between two-factor and the device list, because it belongs to both: a passkey
     is a way in, and it is a thing on a device you might later want to revoke. -->
<section class="panel">
	<header>
		<h2>{t('passkey.title')}</h2>
		<p class="hint">{t('passkey.hint')}</p>
	</header>

	{#if !passkeySupported}
		<p class="hint">{t('passkey.unavailable')}</p>
	{:else}
		{#if passkeys.length}
			<ul class="list">
				{#each passkeys as key (key.id)}
					<li>
						<div class="what">
							<span class="primary-line">{key.name}</span>
							<span class="secondary">
								{key.user_verified ? t('passkey.bothFactors') : t('passkey.possessionOnly')}
								·
								{key.last_used_at ? t('passkey.lastUsed', { when: ago(key.last_used_at) }) : t('passkey.neverUsed')}
							</span>
						</div>
						<button class="secondary" onclick={() => removePasskey(key)}>{t('passkey.remove')}</button>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="empty">{t('passkey.none')}</p>
		{/if}

		<form class="row" onsubmit={addPasskey}>
			<input bind:value={passkeyName} placeholder={t('passkey.namePlaceholder')} aria-label={t('passkey.name')} />
			<button class="primary" type="submit" disabled={registering}>
				{registering ? t('passkey.registering') : t('passkey.add')}
			</button>
		</form>
		{#if passkeyError}<p class="error">{passkeyError}</p>{/if}
	{/if}
</section>

<section class="panel">
	<header>
		<h2>{t('account.devices')}</h2>
		<p class="hint">
			{t('account.devicesHint')}
		</p>
	</header>

	<ul class="sessions">
		{#each sessions as session (session.id)}
			<li>
				<div class="what">
					<span class="device">
						{session.device}
						{#if session.current}<span class="badge">{t('account.thisDevice')}</span>{/if}
					</span>
					<!-- The address and the time, small: they are what settles "was that
					     me?", and they are not what you read first. -->
					<span class="when" title={session.user_agent}>
						{ago(session.last_seen_at)}{#if session.ip}{' · ' + session.ip}{/if}
					</span>
				</div>
				<button class="secondary" onclick={() => endSession(session)}>{t('account.signOutDevice')}</button>
			</li>
		{/each}
	</ul>
</section>

<!-- Last, and quietly: a donate link belongs in Settings, never in the way of the
     work. A plain anchor with an inline SVG — no third-party script, no webfont,
     no tracking — so it works offline and cannot phone home. See the coffee-cup
     snippet for why not their generated button. -->
<section class="panel">
	<header>
		<h2>{t('account.support')}</h2>
		<p class="hint">{t('account.supportHint')}</p>
	</header>

	<a
		class="bmc"
		href="https://buymeacoffee.com/kristianwind"
		target="_blank"
		rel="noopener noreferrer"
	>
		<svg
			width="16"
			height="16"
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			stroke-width="1.8"
			stroke-linecap="round"
			stroke-linejoin="round"
			aria-hidden="true"
			focusable="false"
		>
			<path d="M4 9h13v6a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4V9Z" />
			<path d="M17 10h1.5a2.5 2.5 0 0 1 0 5H17" />
			<path d="M7.5 5.6c0-.9.8-1.1.8-2M11 5.6c0-.9.8-1.1.8-2M14.5 5.6c0-.9.8-1.1.8-2" />
		</svg>
		{t('account.buyCoffee')} ↗
	</a>
</section>

<style>
	.looks {
		margin-top: var(--s4);
		padding-top: var(--s3);
		border-top: 1px solid var(--line);
	}

	.looks h3 {
		font-size: var(--text-sm);
		margin-bottom: var(--s1);
	}

	.sizes {
		margin-top: var(--s4);
		padding-top: var(--s3);
		border-top: 1px solid var(--line);
	}

	.sizes h3 {
		font-size: var(--text-sm);
		margin-bottom: var(--s1);
	}

	.size-row {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
		gap: var(--s3);
		margin-top: var(--s3);
	}

	.size-field {
		display: flex;
		flex-direction: column;
		gap: var(--s1);
	}

	.size-field label {
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--ink-faint);
	}

	.look-row {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
		gap: var(--s2);
		margin-top: var(--s3);
	}

	.look-card {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: var(--s1);
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		text-align: left;
	}

	.look-card.chosen {
		border-color: var(--accent);
	}

	.look-sample {
		font-family: var(--font);
		font-size: 1.5rem;
		line-height: 1;
		color: var(--ink);
	}

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

	/* On its own white card whatever the theme: a QR is dark-on-light or it does not
	   scan, so the square keeps its own background rather than taking the page's. */
	.qr {
		width: 176px;
		height: 176px;
		padding: var(--s2);
		background: #ffffff;
		border: 1px solid var(--line);
		border-radius: var(--radius);
	}

	.qr :global(svg) {
		display: block;
		width: 100%;
		height: 100%;
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

	/* The understated variant: present, not loud. Bordered and in the muted ink so
	   it reads as a footnote, brightening on hover. The cup is outline-only. */
	.bmc {
		align-self: flex-start;
		display: inline-flex;
		align-items: center;
		gap: var(--s2);
		padding: var(--s2) var(--s3);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		color: var(--ink-muted);
		text-decoration: none;
		font-size: var(--text-sm);
		transition:
			color var(--fast) var(--ease),
			border-color var(--fast) var(--ease);
	}

	.bmc:hover {
		color: var(--ink);
		border-color: var(--ink-muted);
	}

	.bmc:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 2px;
	}

	.bmc svg {
		flex: none;
	}
</style>
