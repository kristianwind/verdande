/**
 * Renders static/icon.svg to the PNGs the manifest and iOS need.
 *
 *     node scripts/generate-icons.js
 *
 * A script rather than a test, so CI never runs it and never rewrites a tracked
 * binary. Run it when the icon changes; the smoke test fetches every icon the
 * manifest names, so a reference with no file behind it fails there.
 *
 * It drives Chromium because there is no SVG renderer on this machine — which is
 * why these references were once removed rather than left pointing at files that
 * did not exist. Playwright is a renderer, and it is already a dependency.
 */
import { chromium } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';

const SIZES = [
	// The two the manifest asks for: 192 for a launcher, 512 for a splash screen.
	{ file: 'static/icon-192.png', size: 192 },
	{ file: 'static/icon-512.png', size: 512 },
	// And the one Safari wants by name. It ignores the manifest's icons entirely
	// and looks for this, which is why an installed PWA on iOS otherwise gets a
	// screenshot of the page as its home-screen icon.
	{ file: 'static/apple-touch-icon.png', size: 180 }
];

const svg = readFileSync('static/icon.svg', 'utf8');
const browser = await chromium.launch();
const page = await browser.newPage();

for (const { file, size } of SIZES) {
	await page.setViewportSize({ width: size, height: size });
	await page.setContent(
		`<style>html,body{margin:0;padding:0}svg{display:block;width:${size}px;height:${size}px}</style>${svg}`
	);
	writeFileSync(file, await page.locator('svg').screenshot());
	console.log(`${file}  ${size}×${size}`);
}

await browser.close();
