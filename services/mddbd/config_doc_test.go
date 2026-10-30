package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// configDocPath is the server's configuration reference.
const configDocPath = "../../docs/config.md"

// configDocNotSettings are names the server's source contains that are not
// settings, so docs/config.md leaves them out: the encryption format's magic
// bytes (notConfigKeys) and the integrity check's child-process marker, which
// the server sets for itself (db_integrity.go).
var configDocNotSettings = map[string]bool{"MDDB_INTERNAL_VERIFY_DB": true}

// configDocOtherComponents are names docs/config.md may mention that another
// component reads, not mddbd: the CLI (services/mddb-cli), the chat service
// (services/mddb-chat), the panel (services/mddb-panel) or a script. Empty
// today; add a name here with the component that reads it.
var configDocOtherComponents = map[string]string{}

// configDocNameRe matches a variable name, or a family written as a prefix
// with a trailing underscore ("MDDB_AUDIT_*").
var configDocNameRe = regexp.MustCompile(`MDDB_[A-Z0-9_]+`)

// TestConfigDocMatchesServer keeps docs/config.md and the server in step
// both ways. Every MDDB_* name the page gives must be read by the server, be
// a family prefix of one it reads, or be listed in configDocOtherComponents;
// every setting the server reads must be on the page. MDDB_CORPUS_MAX_BYTES
// was read but undocumented, and other pages named variables
// (MDDB_JWT_SECRET, MDDB_DB_PATH) the server has never read.
func TestConfigDocMatchesServer(t *testing.T) {
	data, err := os.ReadFile(configDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", configDocPath, err)
	}
	server := serverEnvNames(t)
	docNames := map[string]bool{}
	for _, n := range configDocNameRe.FindAllString(string(data), -1) {
		docNames[n] = true
	}

	var unread, undocumented []string
	for n := range docNames {
		if !server[n] && configDocOtherComponents[n] == "" && !isPrefixOfAny(n, server) {
			unread = append(unread, n)
		}
	}
	for n := range server {
		if !docNames[n] && !notConfigKeys[n] && !configDocNotSettings[n] {
			undocumented = append(undocumented, n)
		}
	}
	sort.Strings(unread)
	sort.Strings(undocumented)
	if len(unread) > 0 {
		t.Errorf("%s names variables mddbd does not read (fix the name, or list the component that reads it in configDocOtherComponents): %s",
			configDocPath, strings.Join(unread, ", "))
	}
	if len(undocumented) > 0 {
		t.Errorf("mddbd reads variables %s does not document: %s", configDocPath, strings.Join(undocumented, ", "))
	}
}

// isPrefixOfAny reports whether name is a family prefix ("MDDB_AUDIT_") of a
// variable in names.
func isPrefixOfAny(name string, names map[string]bool) bool {
	if !strings.HasSuffix(name, "_") {
		return false
	}
	for n := range names {
		if strings.HasPrefix(n, name) {
			return true
		}
	}
	return false
}

func TestIsPrefixOfAny(t *testing.T) {
	names := map[string]bool{"MDDB_AUDIT_ENABLED": true}
	for _, c := range []struct {
		name string
		want bool
	}{
		{"MDDB_AUDIT_", true},
		{"MDDB_AUDIT", false}, // a prefix only counts with its underscore
		{"MDDB_NOPE_", false},
	} {
		if got := isPrefixOfAny(c.name, names); got != c.want {
			t.Errorf("isPrefixOfAny(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
