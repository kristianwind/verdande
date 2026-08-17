/**
 * The account every test runs as.
 *
 * In its own file rather than exported from auth.setup.js, because Playwright
 * refuses to let one test file import another — and both the setup that creates
 * this account and the device-list test that signs in as it a second time need
 * the same credentials.
 */
export const USER = {
	name: 'Kristian',
	email: 'test@example.dk',
	password: 'et langt kodeord til test'
};
