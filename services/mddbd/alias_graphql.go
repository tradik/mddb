package main

import (
	"context"
	"reflect"

	"github.com/99designs/gqlgen/graphql"
)

// GraphQL side of alias resolution (GO-044): one field middleware rewrites
// the parsed arguments of every resolver — a collection argument, or the
// collection fields of an input object — before the resolver reads them.
func (s *Server) aliasFieldMiddleware(ctx context.Context, next graphql.Resolver) (any, error) {
	if fc := graphql.GetFieldContext(ctx); fc != nil && s.Aliases.active() {
		for k, v := range fc.Args {
			fc.Args[k] = s.Aliases.resolveAliasValue(k, v)
		}
	}
	return next(ctx)
}

// Go field names that hold a collection in GraphQL input types.
var aliasGoStringFields = map[string]bool{"Collection": true, "SourceCollection": true}
var aliasGoListFields = map[string]bool{"Collections": true, "TargetCollections": true}

// resolveAliasValue returns an argument with its collection fields resolved.
func (a *AliasManager) resolveAliasValue(name string, v any) any {
	switch x := v.(type) {
	case string:
		if name == "collection" || name == "sourceCollection" {
			return a.Resolve(x)
		}
		return x
	case []string:
		if name == "collections" || name == "targetCollections" {
			out := make([]string, len(x))
			for i, c := range x {
				out[i] = a.Resolve(c)
			}
			return out
		}
		return x
	}
	rv := reflect.ValueOf(v)
	switch {
	case rv.Kind() == reflect.Pointer && !rv.IsNil() && rv.Elem().Kind() == reflect.Struct:
		a.resolveAliasStruct(rv.Elem())
		return v
	case rv.Kind() == reflect.Struct:
		cp := reflect.New(rv.Type()).Elem()
		cp.Set(rv)
		a.resolveAliasStruct(cp)
		return cp.Interface()
	}
	return v
}

// resolveAliasStruct rewrites the collection fields of an addressable struct.
func (a *AliasManager) resolveAliasStruct(sv reflect.Value) {
	t := sv.Type()
	for i := 0; i < t.NumField(); i++ {
		f, name := sv.Field(i), t.Field(i).Name
		switch {
		case aliasGoStringFields[name] && f.Kind() == reflect.String:
			f.SetString(a.Resolve(f.String()))
		case aliasGoStringFields[name] && f.Kind() == reflect.Pointer && !f.IsNil() && f.Elem().Kind() == reflect.String:
			f.Elem().SetString(a.Resolve(f.Elem().String()))
		case aliasGoListFields[name] && f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
			for j := 0; j < f.Len(); j++ {
				f.Index(j).SetString(a.Resolve(f.Index(j).String()))
			}
		}
	}
}
