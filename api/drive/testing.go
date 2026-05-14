package drive

import "github.com/ProtonMail/go-proton-api"

// NewTestLink creates a Link with a test name override for use in tests
// that need working Name() calls without real crypto infrastructure.
// The testName field causes Name() to return the given name directly,
// bypassing decryption.
func NewTestLink(pLink *proton.Link, parent *Link, share *Share, resolver LinkResolver, name string) *Link {
	l := NewLink(pLink, parent, share, resolver)
	l.testName = name
	return l
}

// NewTestClient creates a Client with a pre-populated link table for use
// in tests that need GetLink lookups without real API infrastructure.
// The links map is keyed by LinkID.
func NewTestClient(links map[string]*Link) *Client {
	table := make(map[string]*Link, len(links))
	for k, v := range links {
		table[k] = v
	}
	return &Client{
		linkTable: table,
	}
}
