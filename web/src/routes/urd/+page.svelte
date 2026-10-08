<script>
	/**
	 * Urds dør.
	 *
	 * Serveren svarer på /urd med sin egen skal — egen titel, eget mærke og et link
	 * til sit eget manifest — og det er dét, styresystemet installerer. Siden her er
	 * hvad appen gør, når den så er startet: husker hvilken dør den kom ind ad, og
	 * går videre til noterne.
	 *
	 * Ansigtet sættes ikke her, men i layoutet, hvor begge døre står ved siden af
	 * hinanden — `/urd` sætter urd og `/` sætter verdande. Delt op, fordi en regel
	 * med to halvdele, der ligger i hver sin fil, er en regel, hvor kun den ene
	 * halvdel bliver vedligeholdt.
	 *
	 * `replaceState`, så telefonens tilbage-gestus ikke fører tilbage til en side,
	 * der kun omdirigerer.
	 */
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';

	onMount(() => goto('/noter', { replaceState: true }));
</script>

<!-- Bevidst tom. Omdirigeringen sker i samme hug som tegningen, så alt her ville
     være et glimt af noget, ingen nåede at læse. -->
<svelte:head><title>urd</title></svelte:head>
