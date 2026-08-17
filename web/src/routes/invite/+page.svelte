<script>
	/**
	 * The address an invite link points at.
	 *
	 * It exists as a route because the server has been emailing this URL since
	 * invites were built, and until now nothing answered for it: the SPA fallback
	 * served the app shell, the shell found no session, and the recipient was shown
	 * a plain sign-in form for an account they did not have yet. A 200 the whole
	 * way, which is exactly how that class of bug hides here.
	 *
	 * The form is SignIn's: signing up from an invite is one branch of signing in,
	 * and it reads the token out of the URL itself.
	 */
	import SignIn from '$lib/components/SignIn.svelte';
</script>

<svelte:head><title>Invitation · verdande</title></svelte:head>

<SignIn />
