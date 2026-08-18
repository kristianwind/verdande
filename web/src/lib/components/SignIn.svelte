<script>
	/**
	 * Sign-in, first-run setup, the second factor, the invite that creates an
	 * account, and choosing a new password — in one component.
	 *
	 * They are one screen because they are one decision tree: an instance with no
	 * accounts asks you to create one, an instance with accounts asks you to log
	 * in, and a login with 2FA asks for a code. Splitting them across routes would
	 * mean redirecting somebody mid-authentication, which is where sessions get
	 * lost.
	 *
	 * The two that arrive by email — /invite and /reset — do have routes, because a
	 * link has to be an address. Those routes render this component and it reads
	 * the token out of the URL, so the form is the same form and there is one place
	 * where signing in is written down.
	 */
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';
	import { focusOnMount } from '$lib/focus.js';

	// loading | login | setup | totp | forgot | sent | invite | reset | done
	let mode = $state('loading');
	let email = $state('');
	let password = $state('');
	let name = $state('');
	let code = $state('');
	let error = $state('');
	let fields = $state({});
	let busy = $state(false);

	/**
	 * The token from an emailed link, if this is one.
	 *
	 * Read from the URL rather than passed in as a prop: the layout renders this
	 * component in several places and none of them should have to know about
	 * tokens.
	 */
	let token = $derived($page.url.searchParams.get('token') ?? '');
	let linkMode = $derived(
		$page.url.pathname === '/invite' ? 'invite' : $page.url.pathname === '/reset' ? 'reset' : ''
	);

	$effect(() => {
		if (linkMode) {
			mode = linkMode;
			return;
		}
		api
			.setupState()
			.then((state) => (mode = state.needs_setup ? 'setup' : 'login'))
			.catch(() => (mode = 'login'));
	});

	async function run(fn) {
		busy = true;
		error = '';
		fields = {};
		try {
			await fn();
		} catch (e) {
			error = humanMessage(e);
			fields = e.fields ?? {};
		} finally {
			busy = false;
		}
	}

	const submit = (event) => {
		event.preventDefault();
		if (busy) return;

		run(async () => {
			switch (mode) {
				case 'setup':
					await api.setup({
						email,
						name,
						password,
						timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
						locale: navigator.language?.startsWith('en') ? 'en' : 'da'
					});
					await app.load();
					break;

				case 'login': {
					const result = await api.login(email, password);
					if (result.totp_required) {
						mode = 'totp';
						password = '';
						return;
					}
					await app.load();
					break;
				}

				case 'totp':
					await api.loginTOTP(code);
					await app.load();
					break;

				case 'forgot':
					await api.forgotPassword(email);
					mode = 'sent';
					break;

				case 'invite':
					// The email address is not asked for and not sent: it comes off the
					// invite on the server, which is what stops a link being redirected
					// to somebody the inviter did not invite.
					await api.signup({
						token,
						name,
						password,
						timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
						locale: navigator.language?.startsWith('en') ? 'en' : 'da'
					});
					// Signup starts the session itself, so there is nothing to log into.
					await app.load();
					goto('/');
					break;

				case 'reset':
					await api.resetPassword(token, password);
					// Not signed in afterwards, on purpose: the reset ends every session
					// the account had, and quietly starting a new one here would undo
					// the half of that which matters.
					password = '';
					mode = 'done';
					break;
			}
		});
	};
</script>

<div class="screen">
	<form onsubmit={submit}>
		<div class="brand">
			<span class="rune" aria-hidden="true">ᚹ</span>
			<h1>verdande</h1>
		</div>

		{#if mode === 'loading'}
			<p class="lede">&nbsp;</p>
		{:else if mode === 'setup'}
			<p class="lede">Opret den første konto. Den bliver administrator.</p>

			<label>
				Navn
				<input bind:value={name} autocomplete="name" required />
				{#if fields.name}<span class="field-error">Skal udfyldes</span>{/if}
			</label>
			<label>
				E-mail
				<input bind:value={email} type="email" autocomplete="username" required />
				{#if fields.email}<span class="field-error">Skal være en e-mailadresse</span>{/if}
			</label>
			<label>
				Adgangskode
				<input bind:value={password} type="password" autocomplete="new-password" required />
				<span class="hint">Mindst 10 tegn. En sætning er både nemmere og stærkere.</span>
				{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
			</label>

			<button type="submit" disabled={busy}>Opret konto</button>
		{:else if mode === 'totp'}
			<p class="lede">Indtast koden fra din authenticator.</p>

			<label>
				Kode
				<input
					bind:value={code}
					use:focusOnMount
					inputmode="numeric"
					autocomplete="one-time-code"
					placeholder="123456"
					class="code"
					required
				/>
				<span class="hint">Har du mistet din telefon, virker en af dine gendannelseskoder her.</span>
			</label>

			<button type="submit" disabled={busy}>Fortsæt</button>
		{:else if mode === 'forgot'}
			<p class="lede">Vi sender et link til at vælge en ny adgangskode.</p>

			<label>
				E-mail
				<input bind:value={email} type="email" autocomplete="username" required />
			</label>

			<button type="submit" disabled={busy}>Send link</button>
			<button type="button" class="link" onclick={() => (mode = 'login')}>Tilbage</button>
		{:else if mode === 'invite' || mode === 'reset'}
			{#if !token}
				<!-- A link that lost its token on the way. Saying so beats a form that
				     cannot succeed and a message that arrives only after it is filled in. -->
				<p class="lede">
					Linket er ikke komplet. Åbn det fra e-mailen igen, eller bed om et nyt.
				</p>
				<a class="link" href="/">Til log ind</a>
			{:else if app.user}
				<!-- Already somebody else. The invite is tied to an address, so using it
				     from this session would create a second account for a person who is
				     right here in the first one. -->
				<p class="lede">
					Du er logget ind som {app.user.name}. Log ud først, og åbn så linket igen.
				</p>
				<button
					type="button"
					class="link"
					onclick={async () => {
						await api.logout();
						location.reload();
					}}>Log ud</button
				>
			{:else if mode === 'invite'}
				<p class="lede">Du er inviteret. Vælg et navn og en adgangskode.</p>

				<label>
					Navn
					<input bind:value={name} autocomplete="name" required />
					{#if fields.name}<span class="field-error">Skal udfyldes</span>{/if}
				</label>
				<label>
					Adgangskode
					<input bind:value={password} type="password" autocomplete="new-password" required />
					<span class="hint">Mindst 10 tegn. En sætning er både nemmere og stærkere.</span>
					{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
				</label>

				<button type="submit" disabled={busy}>Opret konto</button>
			{:else}
				<p class="lede">Vælg en ny adgangskode.</p>

				<label>
					Ny adgangskode
					<input bind:value={password} type="password" autocomplete="new-password" required />
					<span class="hint">Mindst 10 tegn. Alle andre enheder bliver logget ud.</span>
					{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
				</label>

				<button type="submit" disabled={busy}>Gem adgangskoden</button>
			{/if}
		{:else if mode === 'done'}
			<p class="lede">Adgangskoden er skiftet. Log ind med den nye.</p>
			<a class="link" href="/">Til log ind</a>
		{:else if mode === 'sent'}
			<!-- Deliberately not "we sent an email": that would confirm the address
			     has an account here to anybody who tried it. -->
			<p class="lede">
				Hvis adressen har en konto, er der et link på vej. Tjek også spamfilteret.
			</p>
			<button type="button" class="link" onclick={() => (mode = 'login')}>Tilbage</button>
		{:else}
			<p class="lede">Log ind for at fortsætte.</p>

			<label>
				E-mail
				<input bind:value={email} type="email" autocomplete="username" required />
			</label>
			<label>
				Adgangskode
				<input bind:value={password} type="password" autocomplete="current-password" required />
			</label>

			<button type="submit" disabled={busy}>Log ind</button>
			<button type="button" class="link" onclick={() => (mode = 'forgot')}>
				Glemt adgangskode?
			</button>
		{/if}

		{#if error}
			<p class="error" role="alert">{error}</p>
		{/if}
	</form>
</div>

<style>
	.screen {
		height: 100dvh;
		display: grid;
		place-items: center;
		padding: var(--s4);
		background: var(--ground);
	}

	form {
		width: 100%;
		max-width: 340px;
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

	h1 {
		font-size: var(--text-xl);
		font-weight: 560;
		letter-spacing: -0.02em;
	}

	.lede {
		margin: 0;
		text-align: center;
		color: var(--ink-muted);
		font-size: var(--text-sm);
	}

	label {
		display: flex;
		flex-direction: column;
		gap: var(--s2);
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}

	input {
		padding: var(--s3);
		background: var(--surface);
		border: 1px solid var(--line);
		border-radius: var(--radius);
		color: var(--ink);
		outline: none;
		transition: border-color var(--fast) var(--ease);
	}

	input:focus {
		border-color: var(--accent);
	}

	.code {
		font-family: var(--font-mono);
		font-size: var(--text-lg);
		letter-spacing: 0.2em;
		text-align: center;
	}

	.hint,
	.field-error {
		font-size: var(--text-xs);
	}

	.hint {
		color: var(--ink-faint);
	}

	.field-error {
		color: var(--danger);
	}

	button[type='submit'] {
		padding: var(--s3);
		background: var(--accent);
		color: var(--accent-ink);
		border-radius: var(--radius);
		font-weight: 550;
		transition: background var(--fast) var(--ease);
	}

	button[type='submit']:hover:not(:disabled) {
		background: var(--accent-hover);
	}

	button[type='submit']:disabled {
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

	.error {
		margin: 0;
		padding: var(--s3);
		background: var(--danger-sunken);
		border-radius: var(--radius);
		color: var(--danger);
		font-size: var(--text-sm);
		text-align: center;
	}
</style>
