/**
 * Danish, and the source of truth.
 *
 * This is the language the app was written in and the one whose phrasing was
 * argued over; `en.js` is a translation of it. A key missing from English falls
 * through to here rather than to the key itself, because a Danish sentence in an
 * English screen can be read and `settings.notifications.title` cannot.
 *
 * Keys are grouped by where they are read, not by what they say. Two screens that
 * happen to use the word "Slet" get a key each: they are two decisions, and the
 * day one of them needs to say "Fjern" instead, a shared key is a rewrite of both.
 *
 * `{name}` placeholders are filled by `t()`. They exist because word order is not
 * a constant across languages — "3 tasks left" and "3 opgaver tilbage" happen to
 * agree, "shared with me" and "delt med mig" do not.
 */
export const da = {
	// --- the shell ---------------------------------------------------------------
	'nav.today': 'I dag',
	'nav.upcoming': 'Kommende',
	'nav.delegated': 'Venter på andre',
	'nav.inbox': 'Indbakke',
	'nav.settings': 'Indstillinger',
	'nav.signOut': 'Log ud',
	'nav.main': 'Hovedmenu',
	'nav.search': 'Søg',
	'nav.searchLong': 'Søg i opgaver og projekter',
	'nav.noResults': 'Ingen resultater.',
	'nav.showSidebar': 'Vis sidebjælken',
	'nav.hideSidebar': 'Skjul sidebjælken',
	'nav.showMenu': 'Vis menu',
	'nav.sidebarWidth': 'Bredde på sidebjælken',
	'nav.toggleTheme': 'Skift mellem lyst og mørkt tema',
	'nav.offline': 'Offline',
	'nav.offlineHint': 'Ændringer fra andre vises ikke lige nu',
	'nav.doItToday': 'Gør det i dag',

	// --- the sidebar's own sections ------------------------------------------------
	'sidebar.projects': 'Projekter',
	'sidebar.shared': 'Delt med mig',
	'sidebar.filters': 'Filtre',
	'sidebar.labels': 'Etiketter',
	'sidebar.newProject': 'Nyt projekt',
	'sidebar.newGroup': 'Ny gruppe',
	'sidebar.projectName': 'Projektnavn',
	'sidebar.groupName': 'Gruppens navn',
	'sidebar.noProjects': 'Ingen projekter endnu.',
	'sidebar.emptyGroup': 'Tom — træk et projekt herop.',
	'sidebar.rename': 'Omdøb',
	'sidebar.delete': 'Slet',
	'sidebar.fold': 'Fold {name} sammen',
	'sidebar.unfold': 'Fold {name} ud',
	'sidebar.deleteGroup': 'Slet gruppen "{name}"?',
	'sidebar.deleteGroupOne': 'Projektet i den bliver, men ligger bagefter uden gruppe.',
	'sidebar.deleteGroupMany': 'De {n} projekter bliver, men ligger bagefter uden gruppe.',

	// --- a task in a list ----------------------------------------------------------
	'task.add': 'Tilføj',
	'task.new': 'Ny opgave',
	'task.placeholder': 'Tilføj en opgave — prøv “betal moms i morgen kl 10 p1 #Firma”',
	'task.complete': 'Markér som færdig',
	'task.reopen': 'Genåbn opgave',
	'task.today': 'I dag',
	'task.tomorrow': 'I morgen',
	'task.yesterday': 'I går',
	'task.overdue': '{n} dage forsinket',
	'task.repeats': 'Gentages {rule}',
	'task.assignee': 'Ansvarlig: {name}',
	'task.subtasks': '{done} af {total} undertasks er lukket',
	'task.attachments': '{n} vedhæftet',
	'task.priority': 'Prioritet {n}',
	'task.noPriority': 'Ingen prioritet',

	// --- a project group -----------------------------------------------------------
	'group.colorOf': 'Farve på {name}'
};
