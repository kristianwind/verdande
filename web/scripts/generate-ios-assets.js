/**
 * Renders the iOS app's icon and launch image from static/icon.svg.
 *
 *     node scripts/generate-ios-assets.js
 *
 * Same reasoning as generate-icons.js: a script, not a test, run when the icon
 * changes, with Chromium as the SVG renderer because it is already here.
 *
 * Two things differ from the web icons, and both are Apple's rules rather than
 * taste. The app icon is square with no transparency — iOS draws the rounded
 * corners itself, and rounded corners already in the image show as a dark ring
 * inside its own mask — so the tile's `rx` is dropped and the result flattened.
 * And the launch image is the rune small on the tile colour, the size
 * Capacitor's template asks for, so the app opens on the colour it is about to
 * draw instead of on white.
 */
import { chromium } from '@playwright/test';
import { readFileSync, writeFileSync } from 'node:fs';

const ICONS = '../app/ios/App/App/Assets.xcassets';
const svg = readFileSync('static/icon.svg', 'utf8').replace(/ rx="[^"]*"/, '');
const tile = /<rect[^>]*fill="(#[0-9a-fA-F]{6})"/.exec(svg)?.[1];
if (!tile) throw new Error('static/icon.svg has no tile colour to paint the launch image with');

const browser = await chromium.launch();
const page = await browser.newPage();

async function render(file, size, markSize) {
	await page.setViewportSize({ width: size, height: size });
	await page.setContent(
		`<style>html,body{margin:0;padding:0;background:${tile}}` +
			`div{width:${size}px;height:${size}px;display:grid;place-items:center}` +
			`svg{display:block;width:${markSize}px;height:${markSize}px}</style><div>${svg}</div>`
	);
	writeFileSync(file, await page.locator('div').screenshot({ omitBackground: false }));
	console.log(`${file}  ${size}×${size}`);
}

await render(`${ICONS}/AppIcon.appiconset/AppIcon-512@2x.png`, 1024, 1024);
// Capacitor's template names three files for the one launch image; all the same.
for (const name of ['splash-2732x2732.png', 'splash-2732x2732-1.png', 'splash-2732x2732-2.png']) {
	await render(`${ICONS}/Splash.imageset/${name}`, 2732, 640);
}

await browser.close();
