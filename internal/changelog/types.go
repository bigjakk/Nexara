// Package changelog fetches release notes from GitHub Releases and parses
// them into a structured format the frontend changelog popup can render.
package changelog

// ChangeType classifies a highlight by the release-note section it was found
// under, so the dialog can chip it "New", "Fix" and so on.
//
// It is empty whenever the body offered nothing to classify by — a curated
// "Highlights" section, or a body with no headings at all. The dialog shows no
// chip in that case rather than guessing one, which is why the zero value has
// to stay meaningful.
type ChangeType string

// The chip vocabulary. It is deliberately coarser than the conventional-commit
// prefixes feeding it: an operator reading the popup cares that something is
// new or fixed, not whether it reached them as a refactor or a perf commit.
const (
	ChangeNew      ChangeType = "new"
	ChangeImproved ChangeType = "improved"
	ChangeFix      ChangeType = "fix"
	ChangeSecurity ChangeType = "security"
	ChangeDocs     ChangeType = "docs"
	// ChangeBreaking comes from the bullet rather than its heading: a `!`
	// conventional-commit marker. generate-release-notes.sh cannot match a `!`
	// subject, so these land under "Other Changes" with no heading to classify
	// them, and they are the entries an operator most needs to see.
	ChangeBreaking ChangeType = "breaking"
)

// allChangeTypes is every type the parser can emit. It drives the wire-format
// table and the cross-language guard, so adding a constant above without adding
// it here — or without adding it to the frontend — fails the build rather than
// silently rendering a chipless row.
var allChangeTypes = []ChangeType{
	ChangeNew,
	ChangeImproved,
	ChangeFix,
	ChangeSecurity,
	ChangeDocs,
	ChangeBreaking,
}

// Highlight is one bullet shown as a row in the "What's New" dialog.
type Highlight struct {
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Type        ChangeType `json:"type,omitempty"`
}

// Entry is a single release with its parsed highlights.
//
// MoreCount is how many further highlights the release body had that the cap
// dropped. It is 0 for essentially every real release; when it is not, the
// dialog says so and points at the full notes rather than ending the list
// without explanation.
type Entry struct {
	Version    string      `json:"version"`
	Date       string      `json:"date"`
	Highlights []Highlight `json:"highlights"`
	URL        string      `json:"url,omitempty"`
	MoreCount  int         `json:"more_count,omitempty"`
}
