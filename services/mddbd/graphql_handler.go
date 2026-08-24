package main

import (
	"context"
	"fmt"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/99designs/gqlgen/graphql/playground"
	"log/slog"
	gql "mddb/graphql"
	"net/http"
)

// newGraphQLHandler creates the GraphQL HTTP handler
func (s *Server) newGraphQLHandler() http.Handler {
	// Create adapter to bridge Server and GraphQL resolvers
	adapter := NewGraphQLServerAdapter(s)

	// Create GraphQL server with generated schema
	config := gql.Config{
		Resolvers: gql.NewResolver(adapter),
	}

	// Add directive implementations
	config.Directives.Auth = gql.AuthDirective
	config.Directives.HasRole = gql.HasRoleDirective

	srv := handler.New(gql.NewExecutableSchema(config))

	// Configure transports
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.Options{})

	// Add extensions
	srv.Use(extension.Introspection{})

	// Add query complexity limit
	srv.Use(extension.FixedComplexityLimit(1000))

	// Recovery handler for panics
	srv.SetRecoverFunc(func(ctx context.Context, err interface{}) error {
		slog.Warn("GraphQL panic recovered", "err", err)
		return fmt.Errorf("internal server error")
	})

	return srv
}

// GraphQLAuthMiddleware extracts JWT/API key and injects into context
// This middleware is applied before the GraphQL handler
func (s *Server) GraphQLAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Skip auth if not enabled
		if s.AuthManager == nil || !s.AuthManager.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Try to extract and validate token
		// The existing auth middleware already does this, so if we're here
		// and claims exist in context, we just pass them through

		// Try to get claims from existing middleware
		if claims, ok := GetClaimsFromContext(r.Context()); ok {
			// Claims already in context from HTTP middleware
			ctx = context.WithValue(ctx, authContextKey, claims)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// newGraphQLPlaygroundHandler creates the GraphQL Playground handler
func newGraphQLPlaygroundHandler(graphqlPath string) http.Handler {
	return playground.Handler("MDDB GraphQL Playground", graphqlPath)
}
