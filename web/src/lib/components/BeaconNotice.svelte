<script>
	/**
	 * Beskeden om, at denne installation melder ind — sagt uopfordret, første gang
	 * en administrator er logget ind.
	 *
	 * Beaconet er slået til som udgangspunkt, og det kan kun forsvares, hvis det
	 * bliver sagt af sig selv. Siden under Indstillinger → Data siger det rigtigt
	 * og fyldestgørende, og den bliver åbnet af dem, der i forvejen har en
	 * mistanke om, at der er noget at læse; for alle andre er "du kan slå den fra"
	 * et tilbud til nogen, der ikke ved, der er noget at slå fra.
	 *
	 * De tre værdier står i beskeden — det rigtige id, den rigtige version, den
	 * rigtige adresse — frem for ordene "anonyme data". Et løfte om telemetri er
	 * præcis så meget værd som læserens mulighed for at efterprøve det.
	 *
	 * Kun til administratorer, og hentet på `app.user` frem for i onMount: en
	 * indlæsning, der kører, mens login-siden står, kører aldrig for den, der lige
	 * er logget ind — og så er beskeden aldrig sagt til dem, den er skrevet til.
	 */
	import { app } from '$lib/stores.svelte.js';
	import { api } from '$lib/api.js';
	import { t } from '$lib/i18n.svelte.js';
	import { humanMessage } from '$lib/api.js';

	let notice = $state(null);
	let busy = $state(false);

	$effect(() => {
		const me = app.user;
		if (!me?.is_admin) {
			notice = null;
			return;
		}
		let alive = true;
		api
			.beaconNotice()
			.then((r) => {
				if (alive && r?.pending) notice = r;
			})
			.catch(() => {
				// En besked, der ikke kunne hentes, må ikke stå i vejen for appen.
				// Den er stadig ubesvaret næste gang, og bliver stillet igen.
			});
		return () => {
			alive = false;
		};
	});

	async function answer(keep) {
		if (busy) return;
		busy = true;
		try {
			await api.answerBeaconNotice(keep);
			notice = null;
			app.toast(keep ? t('beacon.noticeKept') : t('beacon.noticeStopped'));
		} catch (e) {
			app.toast(humanMessage(e));
		} finally {
			busy = false;
		}
	}
</script>

{#if notice}
	<aside class="notice" role="region" aria-label={t('beacon.noticeTitle')}>
		<h2>{t('beacon.noticeTitle')}</h2>
		<p>{t('beacon.noticeBody')}</p>

		<!-- Det, der bliver sendt, med de rigtige værdier i. Ikke et eksempel: det
		     er selve meldingen, og den kan sammenholdes med indstillingssiden. -->
		<pre>{JSON.stringify(
				{ instance_id: notice.instance_id, version: notice.version },
				null,
				2
			)}</pre>
		<p class="to">{t('beacon.noticeTo', { url: notice.collector_url })}</p>

		<div class="row">
			<button class="button" disabled={busy} onclick={() => answer(true)}>
				{t('beacon.noticeKeep')}
			</button>
			<button class="secondary" disabled={busy} onclick={() => answer(false)}>
				{t('beacon.noticeStop')}
			</button>
			<a href="/indstillinger/data">{t('beacon.noticeMore')}</a>
		</div>
	</aside>
{/if}

<style>
	/* Øverst i indholdet, i flowet — ikke svævende over det. En rude, der ligger
	   oven på arbejdet, dækker knapper, og en besked, der er i vejen, bliver
	   klikket væk uden at blive læst. Den her skubber i stedet, står til den
	   bliver besvaret, og forsvinder så for altid. */
	.notice {
		margin: 0 0 var(--s3);
		max-width: 44rem;
		padding: var(--s3);
		border: 1px solid var(--line);
		border-radius: var(--radius-lg, var(--radius));
		background: var(--surface-raised, var(--surface));
		box-shadow: var(--shadow-lg, 0 10px 30px rgb(0 0 0 / 0.25));
		font-size: var(--text-sm);
	}

	.notice h2 {
		margin: 0 0 var(--s1);
		font-size: var(--text-sm);
	}

	.notice p {
		margin: 0 0 var(--s2);
		color: var(--ink-muted);
		line-height: 1.5;
	}

	.notice pre {
		margin: 0 0 var(--s1);
		padding: var(--s2);
		border-radius: var(--radius);
		background: var(--surface-sunken, var(--surface));
		font-size: var(--text-xs);
		overflow-x: auto;
	}

	.notice .to {
		font-size: var(--text-xs);
		word-break: break-all;
	}

	.row {
		display: flex;
		align-items: center;
		gap: var(--s2);
		flex-wrap: wrap;
	}

	.row a {
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}
</style>
