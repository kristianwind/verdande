/**
 * Pictures and files from the server, inside the iOS app.
 *
 * Attachments are addressed as `/api/v1/attachments/<id>` — in a note's text, in
 * a task's file list, in the export links — and in a browser that is the whole
 * story: the path is on the page's own origin and the cookie goes with it. In the
 * app neither holds. The path would resolve against capacitor://localhost, and an
 * `<img>` or a link cannot carry the bearer token even when pointed at the right
 * server.
 *
 * So in the app, and only there, this watches the page: an image whose source is
 * an API path is fetched with the token and shown from a blob, and a tap on a
 * link to an API path is answered the same way. Done once for the whole document
 * rather than in each component, because the paths come from many places — a
 * note's own markdown among them — and a component that forgot would show a
 * broken picture only in the app, where nobody runs the tests.
 *
 * The original path is kept in `data-src`. The note editor turns the page back
 * into markdown, and a blob address saved into a note is a picture that is gone
 * the next time anybody opens it; `richtext.js` reads `data-src` first.
 */
import { apiURL, authorize, isNativeApp } from './platform.js';

const API = '/api/v1/';
const blobs = new Map();

async function blobFor(path) {
	if (!blobs.has(path)) {
		const promise = fetch(apiURL(path.slice(API.length - 1)), authorize({ method: 'GET' })).then(
			async (res) => {
				if (!res.ok) throw new Error(`${res.status}`);
				return URL.createObjectURL(await res.blob());
			}
		);
		// A failure is not cached: the next sight of the picture tries again.
		promise.catch(() => blobs.delete(path));
		blobs.set(path, promise);
	}
	return blobs.get(path);
}

function adopt(img) {
	const path = img.getAttribute('src');
	// Our own blob address coming back through the observer is not an API path,
	// so it stops here; a new API path on an image already adopted is taken up.
	if (!path?.startsWith(API) || img.dataset.src === path) return;
	img.dataset.src = path;
	// Nothing until the picture is here, rather than a broken-image glyph.
	img.removeAttribute('src');
	blobFor(path)
		.then((url) => {
			if (img.dataset.src === path) img.src = url;
		})
		.catch(() => {});
}

function scan(root) {
	if (root.nodeType !== 1) return;
	if (root.tagName === 'IMG') adopt(root);
	for (const img of root.querySelectorAll?.('img[src^="/api/v1/"]') ?? []) adopt(img);
}

async function openLink(event) {
	const a = event.target.closest?.('a[href]');
	const path = a?.getAttribute('href');
	if (!path?.startsWith(API)) return;
	event.preventDefault();
	try {
		window.open(await blobFor(path), '_blank');
	} catch {
		// Nothing to open; the request's own failure is what there is to say,
		// and there is no screen here to say it on.
	}
}

/** Called once by the layout. A no-op in a browser. */
export function installNativeMedia() {
	if (!isNativeApp() || typeof document === 'undefined') return;
	scan(document.body);
	new MutationObserver((records) => {
		for (const record of records) {
			if (record.type === 'attributes') adopt(record.target);
			else for (const node of record.addedNodes) scan(node);
		}
	}).observe(document.body, {
		childList: true,
		subtree: true,
		attributes: true,
		attributeFilter: ['src']
	});
	document.addEventListener('click', openLink, true);
}
