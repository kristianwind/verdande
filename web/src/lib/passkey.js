/**
 * The browser half of a passkey ceremony.
 *
 * WebAuthn passes binary as ArrayBuffers and JSON cannot carry those, so every
 * boundary between this and the server is a base64url conversion. Getting one of
 * them wrong produces a signature that will not verify, reported as "that key was
 * not accepted" — which reads as a broken key rather than a broken encoder. That
 * is the whole reason this is one file and not three call sites.
 */
import { api } from './api.js';

/** Whether this browser can do it at all. */
export function supported() {
	return typeof window !== 'undefined' && Boolean(window.PublicKeyCredential);
}

/**
 * Whether a key can be offered without the person typing anything first.
 *
 * Not the same question as `supported()`: a browser can do WebAuthn and still have
 * nowhere to put a discoverable credential. Asking first is what stops the sign-in
 * page offering a button that cannot work.
 */
export async function available() {
	if (!supported()) return false;
	try {
		return await window.PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable();
	} catch {
		return false;
	}
}

const toBuffer = (value) => {
	const normalised = value.replace(/-/g, '+').replace(/_/g, '/');
	const padded = normalised.padEnd(normalised.length + ((4 - (normalised.length % 4)) % 4), '=');
	return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0));
};

const toBase64URL = (buffer) =>
	btoa(String.fromCharCode(...new Uint8Array(buffer)))
		.replace(/\+/g, '-')
		.replace(/\//g, '_')
		.replace(/=+$/, '');

/** Registers a key on the signed-in account. Returns the row the server stored. */
export async function register(name) {
	const { challenge_id, options } = await api.beginPasskeyRegistration();
	const publicKey = {
		...options.publicKey,
		challenge: toBuffer(options.publicKey.challenge),
		user: { ...options.publicKey.user, id: toBuffer(options.publicKey.user.id) },
		excludeCredentials: (options.publicKey.excludeCredentials ?? []).map((c) => ({
			...c,
			id: toBuffer(c.id)
		}))
	};

	const credential = await navigator.credentials.create({ publicKey });
	if (!credential) throw new Error('cancelled');

	return api.finishPasskeyRegistration({
		challenge_id,
		name,
		credential: {
			id: credential.id,
			rawId: toBase64URL(credential.rawId),
			type: credential.type,
			response: {
				clientDataJSON: toBase64URL(credential.response.clientDataJSON),
				attestationObject: toBase64URL(credential.response.attestationObject)
			},
			clientExtensionResults: credential.getClientExtensionResults()
		}
	});
}

/**
 * Signs in with a key.
 *
 * No email is asked for and none is sent: the device knows which account its key
 * belongs to. That is not only convenience — it means the sign-in page cannot be
 * used to find out who has an account here.
 */
export async function signIn() {
	const { challenge_id, options } = await api.beginPasskeyLogin();
	const publicKey = {
		...options.publicKey,
		challenge: toBuffer(options.publicKey.challenge),
		allowCredentials: (options.publicKey.allowCredentials ?? []).map((c) => ({
			...c,
			id: toBuffer(c.id)
		}))
	};

	const credential = await navigator.credentials.get({ publicKey });
	if (!credential) throw new Error('cancelled');

	return api.finishPasskeyLogin({
		challenge_id,
		credential: {
			id: credential.id,
			rawId: toBase64URL(credential.rawId),
			type: credential.type,
			response: {
				clientDataJSON: toBase64URL(credential.response.clientDataJSON),
				authenticatorData: toBase64URL(credential.response.authenticatorData),
				signature: toBase64URL(credential.response.signature),
				userHandle: credential.response.userHandle
					? toBase64URL(credential.response.userHandle)
					: null
			},
			clientExtensionResults: credential.getClientExtensionResults()
		}
	});
}
