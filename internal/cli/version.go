package cli

import "golang.org/x/mod/semver"

// versionNewer returns true if latest is a valid semver tag strictly newer than
// current. If either string is not a valid semver (e.g. "dev") it falls back to
// string inequality so dev builds still show the update notice.
func versionNewer(current, latest string) bool {
	if !semver.IsValid(latest) {
		return false
	}
	if !semver.IsValid(current) {
		return current != latest
	}
	return semver.Compare(latest, current) > 0
}
