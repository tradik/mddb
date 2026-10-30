package main

import (
	"context"
	"reflect"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// graphqlAccessRules classifies every root field of the GraphQL schema, as
// "Query.<field>" and "Mutation.<field>" (SEC-019). The collection is read
// from the field's arguments: a collection argument, or the collection fields
// of an input object, after alias resolution.
//
// TestEveryGraphQLFieldIsClassified walks the schema and fails on a root field
// missing here.
var graphqlAccessRules = map[string]accessRule{
	"Query.document":         ruleRead,
	"Query.search":           ruleRead,
	"Query.vectorSearch":     ruleRead,
	"Query.fts":              ruleRead,
	"Query.codeGraph":        ruleRead,
	"Query.codeSymbol":       ruleRead,
	"Query.schema":           ruleRead,
	"Query.indexingStatus":   ruleRead, // none named: the server's queue
	"Query.stats":            ruleAllRead,
	"Query.vectorStats":      ruleAllRead,
	"Query.schemas":          ruleAllRead,
	"Query.me":               ruleAuthenticated,
	"Query.webhooks":         ruleAdmin,
	"Query.users":            ruleAdmin,
	"Query.groups":           ruleAdmin,
	"Query.userPermissions":  ruleAdmin,
	"Query.groupPermissions": ruleAdmin,

	"Mutation.addDocument":      ruleWrite,
	"Mutation.updateDocument":   ruleWrite,
	"Mutation.deleteDocument":   ruleWrite,
	"Mutation.addBatch":         ruleWrite,
	"Mutation.ingestDocuments":  ruleWrite,
	"Mutation.vectorReindex":    ruleWrite,
	"Mutation.setTTL":           ruleWrite,
	"Mutation.importURL":        ruleWrite,
	"Mutation.validateDocument": ruleRead,
	// Deleting a collection has always needed admin on it here.
	"Mutation.deleteCollection":   {class: accessCollection, op: PermAdmin},
	"Mutation.login":              rulePublic,
	"Mutation.createAPIKey":       ruleAuthenticated, // the caller's own key
	"Mutation.registerWebhook":    ruleAdmin,
	"Mutation.deleteWebhook":      ruleAdmin,
	"Mutation.setSchema":          ruleAdmin,
	"Mutation.deleteSchema":       ruleAdmin,
	"Mutation.register":           ruleAdmin,
	"Mutation.setPermission":      ruleAdmin,
	"Mutation.createGroup":        ruleAdmin,
	"Mutation.updateGroup":        ruleAdmin,
	"Mutation.deleteGroup":        ruleAdmin,
	"Mutation.setGroupPermission": ruleAdmin,
}

// accessFieldMiddleware gates the root fields of an operation. Fields below
// the root resolve data the root field was already allowed to return, and
// introspection describes the schema, not data.
func (s *Server) accessFieldMiddleware(ctx context.Context, next graphql.Resolver) (any, error) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || (fc.Object != "Query" && fc.Object != "Mutation") || strings.HasPrefix(fc.Field.Name, "__") {
		return next(ctx)
	}
	key := fc.Object + "." + fc.Field.Name
	var names []string
	for name, v := range fc.Args {
		names = append(names, argCollections(name, v)...)
	}
	if err := s.authorize(ctx, graphqlAccessRules[key], names); err != nil {
		return nil, gqlerror.Errorf("%s: %s", err, key)
	}
	return next(ctx)
}

// argCollections reads the collections in one GraphQL argument: the argument
// itself when it is a collection argument, or the collection fields of an
// input object — the same names alias resolution rewrites.
func argCollections(name string, v any) []string {
	switch x := v.(type) {
	case string:
		if name == "collection" || name == "sourceCollection" {
			return []string{x}
		}
		return nil
	case *string:
		if x != nil && (name == "collection" || name == "sourceCollection") {
			return []string{*x}
		}
		return nil
	case []string:
		if name == "collections" || name == "targetCollections" {
			return x
		}
		return nil
	}
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < rv.NumField(); i++ {
		f, field := rv.Field(i), rv.Type().Field(i).Name
		f = reflect.Indirect(f)
		switch {
		case aliasGoStringFields[field] && f.Kind() == reflect.String:
			out = append(out, f.String())
		case aliasGoListFields[field] && f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
			for j := 0; j < f.Len(); j++ {
				out = append(out, f.Index(j).String())
			}
		}
	}
	return out
}
