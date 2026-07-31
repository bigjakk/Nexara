// Package changelog fetches release notes from GitHub Releases and parses
// them into a structured format the frontend changelog popup can render.
package changelog

// Highlight is one bullet shown as a card in the "What's New" dialog.
type Highlight struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
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
