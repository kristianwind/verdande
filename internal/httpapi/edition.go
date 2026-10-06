package httpapi

import "github.com/kristianwind/verdande/internal/config"

// edition is what this instance tells the world it is.
//
// Never the empty string, even when the config was built by hand in a test and
// never went through config.Load: a flade that reads "" cannot tell "this is the
// full program" from "this build forgot to say", and the first of those is the
// answer that has always been true.
func (s *Server) edition() string {
	if s.cfg.NotesOnly() {
		return config.EditionNotes
	}
	return config.EditionFull
}
