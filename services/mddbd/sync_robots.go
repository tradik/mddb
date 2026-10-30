package main

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// robots.txt for a synced sitemap (INT-022): the pages of a sitemap are read
// only where the site's robots.txt lets the "mddb" agent (or "*") read.

// robotsRule is one Allow or Disallow line of the group a sync follows.
type robotsRule struct {
	allow   bool
	pattern *regexp.Regexp
	length  int // of the path as written: the longest match wins
}

// robotsRules are the rules a sync follows on one host.
type robotsRules []robotsRule

// readRobots fetches root's /robots.txt. A site without one — or whose
// robots.txt cannot be read — sets no rules.
func readRobots(ctx context.Context, root *url.URL) robotsRules {
	res, err := fetchOutbound(ctx, root.Scheme+"://"+root.Host+"/robots.txt", maxRobotsBytes, syncFetchTimeout, syncHeader())
	if err != nil || res.status != http.StatusOK {
		return nil
	}
	return parseRobots(string(res.body))
}

// parseRobots returns the rules of the "mddb" group, or of the "*" group
// when there is none. Patterns take '*' and a closing '$'.
func parseRobots(text string) robotsRules {
	groups := map[string]robotsRules{}
	var agents []string
	inRules := false
	for _, line := range strings.Split(text, "\n") {
		line, _, _ = strings.Cut(line, "#")
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		field, value = strings.ToLower(strings.TrimSpace(field)), strings.TrimSpace(value)
		switch field {
		case "user-agent":
			if inRules { // a new group starts
				agents, inRules = nil, false
			}
			agents = append(agents, strings.ToLower(value))
		case "allow", "disallow":
			inRules = true
			if value == "" {
				continue // "Disallow:" disallows nothing
			}
			rule := robotsRule{allow: field == "allow", pattern: robotsPattern(value), length: len(value)}
			for _, ag := range agents {
				groups[ag] = append(groups[ag], rule)
			}
		}
	}
	if g, found := groups[syncUserAgent]; found {
		return g
	}
	return groups["*"]
}

// robotsPattern compiles a robots.txt path pattern: a prefix, '*' for any
// run of characters, '$' at the end for the end of the path.
func robotsPattern(p string) *regexp.Regexp {
	anchored := strings.HasSuffix(p, "$")
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSuffix(p, "$")), `\*`, ".*")
	if anchored {
		expr += "$"
	}
	return regexp.MustCompile(expr)
}

// allowed reports whether the rules let the sync read path: the longest
// matching rule decides, Allow on a tie, and no match allows.
func (rs robotsRules) allowed(path string) bool {
	best, allow := -1, true
	for _, r := range rs {
		if r.pattern.MatchString(path) && (r.length > best || (r.length == best && r.allow)) {
			best, allow = r.length, r.allow
		}
	}
	return allow
}
