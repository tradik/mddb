package main

import (
	"cmp"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Where a git or issues sync reads from (INT-023), and with which token.
//
// A collection can carry its repository — the link and the stored secret of
// its token — so the code collection holds it once and the rules writing to
// it, or next to it, leave url and tokenSecretRef empty:
//
//	url:   the rule's url → the repository of the collection repositoryFrom
//	       names → the repository of the rule's own collection
//	token: the rule's tokenSecretRef → the tokenSecretRef of the collection
//	       the url came from → the default forge token of the url's host
//	       (forge_tokens.go)
//
// A token is bound to the host it was resolved for: a collection's token
// only goes with that collection's url, a default only to the host it is
// registered for, compared exactly. What reaches the wire is held to the
// same hosts by gitRepo.header and forgeClient (sync_git_forge.go).

// CollectionRepository is the repository a collection is read from: the
// https URL of a git repository on a forge, the forge for a host that is not
// known by name, and the stored secret holding its token. Never a token
// itself.
type CollectionRepository struct {
	URL            string `json:"url,omitempty"`
	Forge          string `json:"forge,omitempty"`
	TokenSecretRef string `json:"tokenSecretRef,omitempty"`
}

// normalize trims the block and refuses what a sync could not read. An
// empty url is a block that removes the stored repository.
func (r *CollectionRepository) normalize() error {
	if r == nil {
		return nil
	}
	r.URL = strings.TrimRight(strings.TrimSpace(r.URL), "/")
	r.Forge = strings.ToLower(strings.TrimSpace(r.Forge))
	r.TokenSecretRef = strings.TrimSpace(r.TokenSecretRef)
	if r.URL == "" {
		if r.TokenSecretRef != "" || r.Forge != "" {
			return errors.New("repository: forge and tokenSecretRef need a url")
		}
		return nil
	}
	if _, err := parseGitRepo(r.URL, r.Forge); err != nil {
		return fmt.Errorf("repository: %w", err)
	}
	if r.TokenSecretRef != "" {
		return validChatKeyName("repository secret", r.TokenSecretRef)
	}
	return nil
}

// checkRepositorySecret refuses a repository whose token names a secret the
// server does not have.
func (s *Server) checkRepositorySecret(r *CollectionRepository) error {
	if r == nil || r.TokenSecretRef == "" || s.ChatScenarios.HasMcpSecret(r.TokenSecretRef) {
		return nil
	}
	return fmt.Errorf("repository secret %q not found: store it first (PUT /v1/chat/mcp-secrets/%s)", r.TokenSecretRef, r.TokenSecretRef)
}

// isForgeSource reports whether a sync reads a forge repository.
func (a *SyncAction) isForgeSource() bool {
	return a.Source == SyncSourceGit || a.Source == SyncSourceIssues
}

// repositoryFromName is the collection whose repository a rule without a
// url reads, for describe.
func (a *SyncAction) repositoryFromName() string {
	return "repository of " + cmp.Or(a.RepositoryFrom, a.Collection)
}

// collectionRepository is the repository a collection carries; nil for none.
func (s *Server) collectionRepository(name string) *CollectionRepository {
	if s.CollectionManager == nil || name == "" {
		return nil
	}
	cfg, ok := s.CollectionManager.Get(s.Aliases.Resolve(name))
	if !ok || cfg.Repository == nil || cfg.Repository.URL == "" {
		return nil
	}
	return cfg.Repository
}

// resolvedSync is a copy of a git or issues sync with its url, forge and
// token filled in as the run reads them; another source, or a rule with
// both of its own, is returned as it is.
func (s *Server) resolvedSync(a *SyncAction) (*SyncAction, error) {
	if a == nil || !a.isForgeSource() {
		return a, nil
	}
	c := *a
	if c.URL == "" {
		repo := s.collectionRepository(c.RepositoryFrom)
		if repo == nil {
			repo = s.collectionRepository(c.Collection)
		}
		if repo == nil {
			return nil, s.errNoRepository(a)
		}
		c.URL, c.Forge = repo.URL, repo.Forge
		// The collection's token goes with the collection's url, and only
		// with it.
		c.TokenSecretRef = cmp.Or(c.TokenSecretRef, repo.TokenSecretRef)
	}
	if c.TokenSecretRef == "" {
		host, err := forgeTokenHost(c.URL, c.Forge)
		if err != nil {
			return nil, err
		}
		c.TokenSecretRef = s.ChatScenarios.ForgeTokenDefault(host)
	}
	return &c, nil
}

// forgeTokenHost is the host a default token is looked up for: the
// repository URL's host, lower case, port included.
func forgeTokenHost(rawURL, forge string) (string, error) {
	repo, err := parseGitRepo(rawURL, forge)
	if err != nil {
		return "", err
	}
	return repo.host, nil
}

// errNoRepository says where the repository of a rule without a url can be
// set.
func (s *Server) errNoRepository(a *SyncAction) error {
	where := fmt.Sprintf("on collection %q", a.Collection)
	if a.RepositoryFrom != "" {
		where = fmt.Sprintf("on collection %q (repositoryFrom) or %q", a.RepositoryFrom, a.Collection)
	}
	return fmt.Errorf("a %s sync needs a repository: set url on the rule, or a repository %s "+
		"(PUT /v1/collection-config with repository.url)", a.Source, where)
}

// checkSyncRepository refuses a git or issues rule whose repository cannot
// be resolved now: one without a url on a manager without a server, or
// whose collections carry none.
func (am *AutomationManager) checkSyncRepository(a *SyncAction) error {
	if !a.isForgeSource() || a.URL != "" {
		return nil
	}
	if am.server == nil {
		return errors.New("a " + a.Source + " sync needs url")
	}
	_, err := am.server.resolvedSync(a)
	return err
}

// collectionsUsingSecret names the collections whose repository token is a
// stored secret, as "collection:<name>", which keeps it from being deleted.
func (s *Server) collectionsUsingSecret(name string) []string {
	if s.CollectionManager == nil {
		return nil
	}
	var out []string
	for coll, cfg := range s.CollectionManager.ListAll() {
		if cfg.Repository != nil && cfg.Repository.TokenSecretRef == name {
			out = append(out, "collection:"+coll)
		}
	}
	sort.Strings(out)
	return out
}
