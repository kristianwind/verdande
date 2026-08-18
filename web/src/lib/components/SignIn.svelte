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
	import { t } from '$lib/i18n.svelte.js';
	import { available, signIn } from '$lib/passkey.js';

	// loading | login | setup | totp | forgot | sent | invite | reset | done
	let mode = $state('loading');
	let email = $state('');
	let password = $state('');
	let name = $state('');
	let code = $state('');
	let error = $state('');
	let fields = $state({});
	let busy = $state(false);

	// --- passkey -----------------------------------------------------------------

	let passkeyReady = $state(false);

	$effect(() => {
		available().then((ok) => (passkeyReady = ok));
	});

	/**
	 * No email is asked for and none is sent: the device knows which account its
	 * key belongs to. That is not only convenience — it means this page cannot be
	 * used to find out who has an account here.
	 */
	async function signInWithPasskey() {
		error = '';
		busy = true;
		try {
			const result = await signIn();
			if (result.totp_required) {
				mode = 'totp';
			} else {
				app.user = result.user;
				await app.load();
			}
		} catch (e) {
			// A cancelled prompt is not a failure. Somebody changed their mind, and
			// an error for that reads as though the key was rejected.
			if (e?.name !== 'NotAllowedError' && e?.message !== 'cancelled') {
				error = humanMessage(e);
			}
		} finally {
			busy = false;
		}
	}

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
			<p class="lede">{t('auth.firstAccount')}</p>

			<label>
				{t('auth.name')}
				<input bind:value={name} autocomplete="name" required />
				{#if fields.name}<span class="field-error">{t('auth.nameRequired')}</span>{/if}
			</label>
			<label>
				{t('auth.email')}
				<input bind:value={email} type="email" autocomplete="username" required />
				{#if fields.email}<span class="field-error">{t('auth.emailInvalid')}</span>{/if}
			</label>
			<label>
				{t('auth.password')}
				<input bind:value={password} type="password" autocomplete="new-password" required />
				<span class="hint">{t('auth.passwordHint')}</span>
				{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
			</label>

			<button type="submit" disabled={busy}>{t('auth.createAccount')}</button>
		{:else if mode === 'totp'}
			<p class="lede">{t('auth.totpPrompt')}</p>

			<label>
				{t('auth.code')}
				<input
					bind:value={code}
					use:focusOnMount
					inputmode="numeric"
					autocomplete="one-time-code"
					placeholder="123456"
					class="code"
					required
				/>
				<span class="hint">{t('auth.totpRecovery')}</span>
			</label>

			<button type="submit" disabled={busy}>{t('auth.continue')}</button>
		{:else if mode === 'forgot'}
			<p class="lede">{t('auth.resetPrompt')}</p>

			<label>
				{t('auth.email')}
				<input bind:value={email} type="email" autocomplete="username" required />
			</label>

			<button type="submit" disabled={busy}>{t('auth.sendLink')}</button>
			<button type="button" class="link" onclick={() => (mode = 'login')}>{t('auth.back')}</button>
		{:else if mode === 'invite' || mode === 'reset'}
			{#if !token}
				<!-- A link that lost its token on the way. Saying so beats a form that
				     cannot succeed and a message that arrives only after it is filled in. -->
				<p class="lede">
					{t('auth.linkBroken')}
				</p>
				<a class="link" href="/">{t('auth.toSignIn')}</a>
			{:else if app.user}
				<!-- Already somebody else. The invite is tied to an address, so using it
				     from this session would create a second account for a person who is
				     right here in the first one. -->
				<p class="lede">
					{t('auth.alreadySignedIn', { name: app.user.name })}
				</p>
				<button
					type="button"
					class="link"
					onclick={async () => {
						await api.logout();
						location.reload();
					}}>{t('auth.signOut')}</button
				>
			{:else if mode === 'invite'}
				<p class="lede">{t('auth.invited')}</p>

				<label>
					{t('auth.name')}
					<input bind:value={name} autocomplete="name" required />
					{#if fields.name}<span class="field-error">{t('auth.nameRequired')}</span>{/if}
				</label>
				<label>
					{t('auth.password')}
					<input bind:value={password} type="password" autocomplete="new-password" required />
					<span class="hint">{t('auth.passwordHint')}</span>
					{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
				</label>

				<button type="submit" disabled={busy}>{t('auth.createAccount')}</button>
			{:else}
				<p class="lede">{t('auth.pickNew')}</p>

				<label>
					{t('auth.newPassword')}
					<input bind:value={password} type="password" autocomplete="new-password" required />
					<span class="hint">{t('auth.newPasswordHint')}</span>
					{#if fields.password}<span class="field-error">{fields.password}</span>{/if}
				</label>

				<button type="submit" disabled={busy}>{t('auth.savePassword')}</button>
			{/if}
		{:else if mode === 'done'}
			<p class="lede">{t('auth.passwordChanged')}</p>
			<a class="link" href="/">{t('auth.toSignIn')}</a>
		{:else if mode === 'sent'}
			<!-- Deliberately not "we sent an email": that would confirm the address
			     has an account here to anybody who tried it. -->
			<p class="lede">
				{t('auth.resetSent')}
			</p>
			<button type="button" class="link" onclick={() => (mode = 'login')}>{t('auth.back')}</button>
		{:else}
			<p class="lede">{t('auth.signInPrompt')}</p>

			<label>
				{t('auth.email')}
				<input bind:value={email} type="email" autocomplete="username" required />
			</label>
			<label>
				{t('auth.password')}
				<input bind:value={password} type="password" autocomplete="current-password" required />
			</label>

			<button type="submit" disabled={busy}>{t('auth.signIn')}</button>

			<!-- Only when the browser has somewhere to keep a key. Offering it where
			     it cannot work is a button that fails for reasons nobody can act on. -->
			{#if passkeyReady}
				<button type="button" class="passkey" disabled={busy} onclick={signInWithPasskey}>
					{busy ? t('passkey.signingIn') : t('passkey.signIn')}
				</button>
			{/if}

			<button type="button" class="link" onclick={() => (mode = 'forgot')}>
				{t('auth.forgot')}
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

	/* Set apart from the primary button but not made secondary: it is an equal way
	   in, not a fallback. */
	.passkey {
		width: 100%;
		padding: var(--s2) var(--s4);
		border: 1px solid var(--line-strong);
		border-radius: var(--radius);
		font-size: var(--text-sm);
		color: var(--ink);
		transition: border-color var(--fast) var(--ease);
	}

	.passkey:hover {
		border-color: var(--accent);
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
