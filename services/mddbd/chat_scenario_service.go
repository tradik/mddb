package main

import (
	"context"
	"errors"
)

// The one implementation of chat scenarios (#403) that REST, gRPC and MCP
// call, so the three surfaces cannot drift: a scenario written over gRPC is
// what REST lists and MCP reads.
//
// Writes need a global administrator — checked by each surface's gate before
// it gets here (access_rules_*.go). Reads are open to any authenticated
// caller who may read every collection the scenario allows, which is also
// how a tenant is confined: its users never see a scenario over another
// tenant's collections.

// errChatScenariosUnavailable: the server was built without the store.
var errChatScenariosUnavailable = errors.New("chat scenarios are not available")

// chatCollectionExists reports whether a name is a collection or an alias.
func (s *Server) chatCollectionExists(name string) bool {
	return s.Aliases.Resolve(name) != name || s.collectionExists(name)
}

// PutChatScenario validates and stores a scenario; created reports whether
// the name was new.
func (s *Server) PutChatScenario(sc *ChatScenario) (created bool, err error) {
	if s.ChatScenarios == nil {
		return false, errChatScenariosUnavailable
	}
	if err := sc.Normalize(s.chatCollectionExists); err != nil {
		return false, err
	}
	return s.ChatScenarios.Put(sc)
}

// DeleteChatScenario removes a scenario by name.
func (s *Server) DeleteChatScenario(name string) error {
	if s.ChatScenarios == nil {
		return errChatScenariosUnavailable
	}
	return s.ChatScenarios.Delete(name)
}

// GetChatScenario returns one scenario the caller may read: ErrForbidden
// (or ErrUnauthorized) when a permission is missing, errChatScenarioNotFound
// when there is no such name.
func (s *Server) GetChatScenario(ctx context.Context, name string) (*ChatScenario, error) {
	if s.ChatScenarios == nil {
		return nil, errChatScenariosUnavailable
	}
	sc, ok := s.ChatScenarios.Get(name)
	if !ok {
		return nil, errChatScenarioNotFound
	}
	if err := s.chatScenarioReadable(ctx, sc); err != nil {
		return nil, err
	}
	return sc, nil
}

// ListChatScenarios returns the scenarios the caller may read, by name.
func (s *Server) ListChatScenarios(ctx context.Context) []*ChatScenario {
	out := []*ChatScenario{}
	if s.ChatScenarios == nil {
		return out
	}
	for _, sc := range s.ChatScenarios.List() {
		if s.chatScenarioReadable(ctx, sc) == nil {
			out = append(out, sc)
		}
	}
	return out
}

// chatScenarioReadable is nil when the caller may read every collection the
// scenario allows. With auth off, everything is readable.
func (s *Server) chatScenarioReadable(ctx context.Context, sc *ChatScenario) error {
	if s.AuthManager == nil {
		return nil
	}
	for _, coll := range sc.AllowedCollections {
		if err := s.AuthManager.CheckPermission(ctx, s.Aliases.Resolve(coll), PermRead); err != nil {
			return err
		}
	}
	return nil
}

// ChatScenariosVersion is the store's version counter: 0 without a store.
func (s *Server) ChatScenariosVersion() uint64 { return s.ChatScenarios.Version() }
