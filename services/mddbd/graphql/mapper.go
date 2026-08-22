package graphql

import (
	"time"
)

// MapMetaInputToInternal converts GraphQL meta input to internal format
func MapMetaInputToInternal(inputs []*MetaInput) map[string][]string {
	if inputs == nil {
		return make(map[string][]string)
	}
	result := make(map[string][]string, len(inputs))
	for _, input := range inputs {
		if input != nil {
			result[input.Key] = input.Values
		}
	}
	return result
}

// MapMetaToGraphQL converts internal meta to GraphQL JSONObject
func MapMetaToGraphQL(meta map[string][]string) map[string]interface{} {
	if meta == nil {
		return make(map[string]interface{})
	}
	result := make(map[string]interface{}, len(meta))
	for k, vals := range meta {
		result[k] = vals
	}
	return result
}

// TimeToInt64 converts time.Time to Unix timestamp (int64)
func TimeToInt64(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
