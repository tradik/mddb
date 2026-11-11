package main

import (
	"net/http"
)

// A module the environment leaves out — MDDB_TEMPORAL or MDDB_SPELL not set,
// so it has no manager — answers its routes with the body GO-049's feature
// switches use (writeFeatureDisabled): 404, {"error":"feature disabled",
// "feature":"<name>"}. Before, its routes were not registered and fell
// through to the mux's plain-text "404 page not found", which a client
// parsing JSON could not read and which looked like a typo in the path.

// handleFeatureDisabled registers paths to answer that feature is off.
func handleFeatureDisabled(mux routeMux, feature string, paths ...string) {
	h := func(w http.ResponseWriter, _ *http.Request) { writeFeatureDisabled(w, feature) }
	for _, p := range paths {
		mux.HandleFunc(p, h)
	}
}
