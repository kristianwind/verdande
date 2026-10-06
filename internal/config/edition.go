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
