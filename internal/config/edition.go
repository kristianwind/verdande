package config

// The two faces of the program. Strings rather than a bool, because a bool named
// `NotesOnly` answers one question and this one will be asked again — and a
// third edition would mean changing every reading of it rather than adding a case.
const (
	EditionFull  = "full"
	EditionNotes = "notes"
)

// NotesOnly is the one question the router asks. Written as a method so the
// comparison lives in one place: `cfg.Edition == "notes"` spelled out at twenty
// call sites is twenty chances to typo a string the compiler does not check.
func (c *Config) NotesOnly() bool { return c.Edition == EditionNotes }

// ProductName and ProductSlug are the two forms of the name the running instance
// calls itself: "Urd" in a sentence, "urd" in a window title, a wordmark and a
// manifest.
//
// Two editions of one program, two names — which is the whole reason the name is
// read from the configuration rather than written into the templates. Verdande is
// what is happening now; Urd, its sister in Völuspá, is what was cut into the wood
// and kept. An instance started without the task routes is a different product to
// whoever uses it, and a product that calls itself by its sibling's name is not a
// product, it is a flag somebody set.
//
// The lowercase form is deliberate and not a styling accident: the wordmark, the
// repository and the window title are all lowercase, so the slug is the name and
// ProductName is the one that gets a capital because it stands in prose.
func (c *Config) ProductName() string {
	if c.NotesOnly() {
		return "Urd"
	}
	return "Verdande"
}

// ProductSlug is ProductName lowercased, kept as its own method rather than as
// strings.ToLower at the call sites: the two are the same transformation today and
// a name that does not lowercase cleanly would otherwise be a bug in every caller
// at once.
func (c *Config) ProductSlug() string {
	if c.NotesOnly() {
		return "urd"
	}
	return "verdande"
}
