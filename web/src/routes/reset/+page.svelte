<script>
	/**
	 * The address a password-reset link points at.
	 *
	 * "Glemt adgangskode?" has always sent one and the API has always accepted the
	 * token — but nothing rendered here, so the link opened the sign-in screen and
	 * a person who had forgotten their password was shown the field they could not
	 * fill in. Same shape as the invite link, and found the same way.
	 */
	import SignIn from '$lib/components/SignIn.svelte';
	// Udgavens navn i titlen. Siden her tegner først, når layoutets `app.load()`
	// har svaret, så udgaven er kendt — også uden en session, hvilket er hele
	// pointen med den her rute.
	import { app } from '$lib/stores.svelte.js';
</script>

<svelte:head><title>Ny adgangskode · {app.productSlug}</title></svelte:head>

<SignIn />
