<script>
	/**
	 * Sign-in, first-run setup and the second factor, in one component.
	 *
	 * They are one screen because they are one decision tree: an instance with no
	 * accounts asks you to create one, an instance with accounts asks you to log
	 * in, and a login with 2FA asks for a code. Splitting them across routes would
	 * mean redirecting somebody mid-authentication, which is where sessions get
	 * lost.
	 */
	import { api, humanMessage } from '$lib/api.js';
	import { app } from '$lib/stores.svelte.js';

	let mode = $state('loading'); // loading | login | setup | totp | forgot | sent
	let email = $state('');
	let password = $state('');
	let name = $state('');
	let code = $state('');
	let error = $state('');
	let fields = $state({});
	let busy = $state(false);

	$effect(() => {
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
				<!-- svelte-ignore a11y_autofocus -->
				<input
					bind:value={code}
					autofocus
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
