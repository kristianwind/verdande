<script>
	/**
	 * Tal en opgave ind.
	 *
	 * En stor rund knap i bunden af skærmen, inden for tommelfingerens rækkevidde.
	 * Den åbner en skuffe med quick-add-feltet og sætter markøren i det, så
	 * tastaturet kommer frem — og så trykker man mikrofonen på tastaturet.
	 *
	 * VI LAVER INGEN TALEGENKENDELSE, og det er et valg frem for en mangel. iOS'
	 * egen diktering kører på enheden på nyere telefoner; en talegenkendelse bygget
	 * ind her ville sende lyden fra et selvhostet program videre til Apple eller
	 * Google, og det er en datavej, der skal forklares på privatlivssiden for at
	 * spare ét tryk. Teksten lander i det felt, der i forvejen viser, hvad parseren
	 * forstod — så "ring til tandlægen i morgen kl 10 p1" bliver læst, mens man ser
	 * på det, og intet oprettes før man siger ja.
	 *
	 * Derfor hedder komponenten diktat og ikke tale: den gør ikke selv noget ved
	 * lyden, den stiller feltet frem.
	 */
	import QuickAdd from './QuickAdd.svelte';
	import { app } from '$lib/stores.svelte.js';
	import { t } from '$lib/i18n.svelte.js';

	let open = $state(false);
	let box;
	let sheet;

	/**
	 * Åbner og sætter fokus I SAMME HUG som klikket.
	 *
	 * Rækkefølgen her er hele funktionen. Safari på iOS åbner kun tastaturet, hvis
	 * `focus()` kaldes inde i den brugerhandling, der bad om det — og Sveltes
	 * opdateringer er asynkrone, så feltet kan ikke være betinget tegnet og få
	 * fokus i samme omgang. Derfor er skuffen ALTID monteret og bare skubbet ud af
	 * syne med `transform` og `opacity`: elementet findes, så det kan fokuseres nu
	 * frem for efter næste opdatering.
	 *
	 * `display: none` og `visibility: hidden` ville begge gøre feltet ufokuserbart,
	 * og `inert` ligeså. Det er grunden til, at den lukkede tilstand er gennemsigtig
	 * og uden pointer-events frem for skjult.
	 */
	function start() {
		// `inert` fjernes med hånden her, og det er ikke sjusk. Attributten sættes
		// reaktivt af `inert={!open}` nedenfor, så den er der stadig i det øjeblik,
		// klikket kører — Sveltes opdateringer er asynkrone. Og et inert element kan
		// ikke fokuseres, så `focusField()` ville ramme ingenting, og iOS ville ikke
		// åbne tastaturet.
		//
		// Den findes, fordi alternativet er værre: uden den kan skuffens knapper
		// tabbes til, mens den er usynlig, på hver eneste side.
		sheet?.removeAttribute('inert');
		open = true;
		box?.focusField();
	}

	function close() {
		open = false;
		// Tastaturet bliver stående, hvis feltet beholder fokus.
		if (sheet?.contains(document.activeElement)) document.activeElement.blur();
	}

	function onkeydown(event) {
		if (event.key === 'Escape' && open) {
			event.preventDefault();
			close();
		}
	}
</script>

<svelte:window {onkeydown} />

<!-- Knappen tegnes ikke i urd-ansigtet: der er ingen opgaver at lægge noget i.
     Og den er skjult over 820px i CSS frem for her, fordi en skærm, der skifter
     bredde, ikke skal kunne tabe et felt, der har fokus. -->
{#if !app.showsUrd}
	<button class="fab" class:away={open} onclick={start} aria-label={t('task.speak')}>
		<!-- En mikrofon, tegnet i samme streger som mærkerne: kapslen, bøjlen under
		     den og foden. Ingen tekst i knappen — den er rund og tommelfingerstor,
		     og navnet står i aria-label. -->
		<svg viewBox="0 0 24 24" aria-hidden="true">
			<rect x="9" y="2.5" width="6" height="11" rx="3" />
			<path d="M5.5 11a6.5 6.5 0 0 0 13 0" />
			<path d="M12 17.5V21" />
		</svg>
	</button>
{/if}

<div
	class="scrim"
	class:open
	aria-hidden="true"
	onclick={close}
	role="presentation"
></div>

<div
	class="sheet"
	class:open
	bind:this={sheet}
	role="dialog"
	aria-modal="false"
	aria-label={t('task.speak')}
	aria-hidden={!open}
	inert={!open}
>
	<div class="grab" aria-hidden="true"></div>
	<p class="hint">{t('task.speakHint')}</p>
	<!-- `marked={false}`: den her må ikke være den, type-anywhere-genvejen finder.
	     Se kommentaren ved `marked` i QuickAdd. -->
	<QuickAdd bind:this={box} marked={false} label={t('task.speak')} onadded={close} />
	<button class="done" onclick={close}>{t('detail.close')}</button>
</div>

<style>
	.fab {
		position: fixed;
		right: max(var(--s4), env(safe-area-inset-right));
		/* Over hjemmeindikatoren på en iPhone, ikke under den. */
		bottom: calc(var(--s4) + env(safe-area-inset-bottom));
		z-index: 40;

		width: 64px;
		height: 64px;
		border: none;
		border-radius: var(--radius-full);
		background: var(--accent);
		color: var(--accent-ink);
		box-shadow: var(--shadow-lg);
		display: grid;
		place-items: center;
		cursor: pointer;
		transition:
			transform var(--fast) var(--ease-out),
			opacity var(--fast) var(--ease-out);
	}

	.fab svg {
		width: 28px;
		height: 28px;
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	.fab:active {
		transform: scale(0.94);
	}

	/* Væk, mens skuffen er åben: den ville ellers stå oven på feltet. */
	.fab.away {
		opacity: 0;
		pointer-events: none;
		transform: translateY(8px);
	}

	.scrim {
		position: fixed;
		inset: 0;
		z-index: 44;
		background: rgb(0 0 0 / 0.4);
		opacity: 0;
		pointer-events: none;
		transition: opacity var(--medium) var(--ease-out);
	}

	.scrim.open {
		opacity: 1;
		pointer-events: auto;
	}

	.sheet {
		position: fixed;
		inset: auto 0 0 0;
		z-index: 45;
		padding: var(--s3) max(var(--s3), env(safe-area-inset-left))
			calc(var(--s3) + env(safe-area-inset-bottom)) max(var(--s3), env(safe-area-inset-right));
		background: var(--surface-raised);
		border-top: 1px solid var(--line);
		border-radius: var(--radius-lg) var(--radius-lg) 0 0;
		box-shadow: var(--shadow-lg);
		display: grid;
		gap: var(--s2);

		/* Lukket: gennemsigtig og skubbet ned, IKKE display:none — feltet skal kunne
		   fokuseres i samme hug som klikket. Se `start()`. */
		opacity: 0;
		transform: translateY(100%);
		pointer-events: none;
		transition:
			transform var(--medium) var(--ease-out),
			opacity var(--medium) var(--ease-out);
	}

	.sheet.open {
		opacity: 1;
		transform: translateY(0);
		pointer-events: auto;
	}

	.grab {
		width: 36px;
		height: 4px;
		border-radius: var(--radius-full);
		background: var(--line-strong);
		justify-self: center;
	}

	.hint {
		margin: 0;
		color: var(--ink-muted);
		font-size: 0.85rem;
		text-align: center;
	}

	.done {
		justify-self: center;
		background: none;
		border: none;
		color: var(--ink-muted);
		padding: var(--s2);
		cursor: pointer;
	}

	/* En skrivebordsskærm har feltet fremme hele tiden og et tastatur at skrive
	   på. Knappen er til tommelfingeren. */
	@media (min-width: 821px) {
		.fab,
		.scrim,
		.sheet {
			display: none;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.fab,
		.scrim,
		.sheet {
			transition: none;
		}
	}
</style>
