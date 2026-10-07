/**
 * Renders each edition's icon SVG to the PNGs the manifest and iOS need.
 *
 *     node scripts/generate-icons.js
 *
 * A script rather than a test, so CI never runs it and never rewrites a tracked
 * binary. Run it when an icon changes; the smoke test fetches every icon the
 * manifest names, so a reference with no file behind it fails there.
 *
 * It drives Chromium because there is no SVG renderer on this machine — which is
 * why these references were once removed rather than left pointing at files that
 * did not exist. Playwright is a renderer, and it is already a dependency.
 *
 * Two sets, because there are two editions. Both ship in the one binary and the
 * server decides which to serve at /icon.svg and friends — see
 * internal/httpapi/shell.go. The alternative, rewriting the manifest to point at
 * urd-icon-192.png, would have left `apple-touch-icon.png` behind: Safari looks
 * for that one BY NAME and ignores the manifest entirely, so a renamed path there
 * is a home-screen icon that is a screenshot of the page.
 */
import { chromium } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';

// The three sizes, and why each is here.
const SIZES = [
	// The two the manifest asks for: 192 for a launcher, 512 for a splash screen.
	{ suffix: '-192.png', size: 192 },
	{ suffix: '-512.png', size: 512 },
	// And the one Safari wants by name.
	{ suffix: '-apple-touch.png', size: 180 }
];

// `out` is spelled out per set rather than built from the prefix, because
// verdande's three were named before there was a second set and renaming them
// would move the paths the manifest and app.html already point at.
const SETS = [
	{
		source: 'static/icon.svg',
		out: {
			'-192.png': 'static/icon-192.png',
			'-512.png': 'static/icon-512.png',
			'-apple-touch.png': 'static/apple-touch-icon.png'
		}
	},
	{
		source: 'static/urd-icon.svg',
		out: {
			'-192.png': 'static/urd-icon-192.png',
			'-512.png': 'static/urd-icon-512.png',
			'-apple-touch.png': 'static/urd-apple-touch-icon.png'
		}
	}
];

const browser = await chromium.launch();
const page = await browser.newPage();

for (const set of SETS) {
	const svg = readFileSync(set.source, 'utf8');
	for (const { suffix, size } of SIZES) {
		const file = set.out[suffix];
		await page.setViewportSize({ width: size, height: size });
		await page.setContent(
			`<style>html,body{margin:0;padding:0}svg{display:block;width:${size}px;height:${size}px}</style>${svg}`
		);
		writeFileSync(file, await page.locator('svg').screenshot());
		console.log(`${file}  ${size}×${size}`);
	}
}

await browser.close();
