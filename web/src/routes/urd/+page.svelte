<script>
	/**
	 * Urds dør.
	 *
	 * Serveren svarer på /urd med sin egen skal — egen titel, eget mærke og et link
	 * til sit eget manifest — og det er dét, styresystemet installerer. Siden her er
	 * hvad appen gør, når den så er startet: husker hvilken dør den kom ind ad, og
	 * går videre til noterne.
	 *
	 * `sessionStorage` og ikke `localStorage`, og det er hele grunden til at de to
	 * apps kan være to apps: de deler origin, så de deler localStorage, cookies og
	 * service worker. sessionStorage er per vindue — og en installeret PWA er sit
	 * eget vindue — så urd-appen har sit ansigt og verdande-appen sit, uden at
	 * træde på hinanden. Det overlever også en genindlæsning, hvilket det skal:
	 * efter et par klik står adressen på /noter, og en F5 dér går ikke gennem /urd
	 * igen.
	 *
	 * `replaceState`, så telefonens tilbage-gestus ikke fører tilbage til en side,
	 * der kun omdirigerer.
	 */
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { app } from '$lib/stores.svelte.js';

	onMount(() => {
		app.enterUrd();
		goto('/noter', { replaceState: true });
	});
</script>

<!-- Bevidst tom. Omdirigeringen sker i samme hug som tegningen, så alt her ville
     være et glimt af noget, ingen nåede at læse. -->
<svelte:head><title>urd</title></svelte:head>
