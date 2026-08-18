/**
 * English, translated from `da.js`.
 *
 * Danish is the source: it is the language the app was written in, and the one
 * whose phrasing was argued over. A key missing here falls through to Danish
 * rather than to the key itself — a Danish sentence in an English screen can be
 * read, and `settings.notifications.title` cannot.
 *
 * Dates are not here. They go through `tag()`, which returns `en-GB` for this
 * locale: the app is day-month everywhere, weeks start on Monday, and an American
 * date format inside a European layout reads as a bug.
 */
export const en = {
	// --- the shell ---------------------------------------------------------------
	'nav.today': 'Today',
	'nav.upcoming': 'Upcoming',
	'nav.delegated': 'Waiting on others',
	'nav.inbox': 'Inbox',
	'nav.settings': 'Settings',
	'nav.signOut': 'Sign out',
	'nav.main': 'Main menu',
	'nav.search': 'Search',
	'nav.searchLong': 'Search tasks and projects',
	'nav.noResults': 'Nothing found.',
	'nav.showSidebar': 'Show the sidebar',
	'nav.hideSidebar': 'Hide the sidebar',
	'nav.showMenu': 'Show menu',
	'nav.sidebarWidth': 'Sidebar width',
	'nav.toggleTheme': 'Switch between light and dark',
	'nav.offline': 'Offline',
	'nav.offlineHint': 'Changes from other people are not showing right now',
	'nav.doItToday': 'Do it today',

	// --- the sidebar's own sections ------------------------------------------------
	'sidebar.projects': 'Projects',
	'sidebar.shared': 'Shared with me',
	'sidebar.filters': 'Filters',
	'sidebar.labels': 'Labels',
	'sidebar.newProject': 'New project',
	'sidebar.newGroup': 'New group',
	'sidebar.projectName': 'Project name',
	'sidebar.groupName': "The group's name",
	'sidebar.noProjects': 'No projects yet.',
	'sidebar.emptyGroup': 'Empty — drag a project up here.',
	'sidebar.rename': 'Rename',
	'sidebar.delete': 'Delete',
	'sidebar.fold': 'Collapse {name}',
	'sidebar.unfold': 'Expand {name}',
	'sidebar.deleteGroup': 'Delete the group "{name}"?',
	'sidebar.deleteGroupOne': 'The project in it stays, and ends up without a group.',
	'sidebar.deleteGroupMany': 'The {n} projects stay, and end up without a group.',

	// --- a task in a list ----------------------------------------------------------
	'task.add': 'Add',
	'task.new': 'New task',
	'task.placeholder': 'Add a task — try “pay VAT tomorrow at 10 p1 #Company”',
	'task.complete': 'Mark as done',
	'task.reopen': 'Reopen task',
	'task.today': 'Today',
	'task.tomorrow': 'Tomorrow',
	'task.yesterday': 'Yesterday',
	'task.overdue': '{n} days overdue',
	'task.repeats': 'Repeats {rule}',
	'task.assignee': 'Assigned to {name}',
	'task.subtasks': '{done} of {total} sub-tasks are done',
	'task.attachments': '{n} attached',
	'task.priority': 'Priority {n}',
	'task.noPriority': 'No priority',

	// --- a project group -----------------------------------------------------------
	'group.colorOf': 'Colour of {name}'
};
