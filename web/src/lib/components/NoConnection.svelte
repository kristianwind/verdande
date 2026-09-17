<script>
	/**
	 * What the app shows when it could not reach the server at all.
	 *
	 * Before this, an outage rendered the sign-in screen: the boot asks who you
	 * are, the request never gets an answer, `user` stays null, and null means
	 * "logged out" everywhere else. So the app blamed the session for the network
	 * being down, and offered a password box that could not possibly work. That is
	 * worse than an error message, because it looks like it might work — people
	 * type the password, get nothing, and conclude their account is broken.
	 *
	 * It happened for real on 17 September 2026: the instance's DNS record was
	 * deleted, and what the app showed was a slow-loading login page.
	 *
	 * The text says the data is safe, because that is the question somebody
	 * actually has when their notes vanish behind a login box.
	 */
	import { t } from '$lib/i18n.svelte.js';
	import { app } from '$lib/stores.svelte.js';

	let trying = $state(false);

	async function again() {
		trying = true;
		try {
			await app.load();
		} finally {
			trying = false;
		}
	}
</script>

<div class="screen">
	<div class="card">
		<div class="brand">
			<span class="rune" aria-hidden="true">ᚹ</span>
			<h1>verdande</h1>
		</div>

		<h2>{t('net.downTitle')}</h2>
		<p class="lede">{t('net.downBody')}</p>

		<button onclick={again} disabled={trying}>
			{trying ? t('net.reconnecting') : t('net.downRetry')}
		</button>
	</div>
</div>

<style>
	.screen {
		height: 100dvh;
		display: grid;
		place-items: center;
		padding: var(--s4);
		background: var(--ground);
	}

	.card {
		width: 100%;
		max-width: 340px;
		display: flex;
		flex-direction: column;
		gap: var(--s4);
		text-align: center;
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
		margin: 0;
	}

	h2 {
		font-size: var(--text-lg);
		margin: 0;
	}

	.lede {
		margin: 0;
		color: var(--ink-soft);
		line-height: 1.5;
	}
</style>
