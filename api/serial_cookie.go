package api

// SerialCookie holds the minimal fields needed to reconstruct an http.Cookie
// for jar injection. Expiry is not persisted — the API server manages cookie
// lifetime. Exported so api/account/ can use it in SessionCredentials.
type SerialCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}
