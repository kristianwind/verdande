package store

// The colours a project, group, label or filter may be marked with.
//
// Names rather than hex values, and the reason is what happens later: a hex in
// ten thousand rows is a decision nobody can revisit, while a name is resolved by
// the interface at the moment it paints and can be retuned for a new theme, a new
// palette, or a display nobody has yet. It is also the difference between a
// database that stores a fact and one that stores a rendering.
//
// The list is closed. An unknown name would be stored happily and then paint as
// the default, which looks like a colour that did not save — so it is refused at
// the edge instead. web/src/lib/colors.js carries the same ids for the picker, and
// a test reads that file to check the two have not drifted.
var Colors = []string{
	"graphite",
	"tomato",
	"amber",
	"lime",
	"green",
	"teal",
	"blue",
	"indigo",
	"violet",
	"magenta",
}

// DefaultColor is what an unmarked thing is, and what the schema defaults to.
const DefaultColor = "graphite"

// ValidColor reports whether a name is one this app can paint.
func ValidColor(name string) bool {
	for _, c := range Colors {
		if c == name {
			return true
		}
	}
	return false
}
