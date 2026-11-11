package main

import (
	"os"
	"strings"
)

// Features (GO-049): optional modules an administrator switches on and off.
// The list is fixed here; what is stored is only each module's switch
// (feature_manager.go). Core — documents, collections, full-text and vector
// search, auth, backups — is not a feature and cannot be switched off.
//
// What a disabled module means on each surface lives in feature_gates.go:
// its REST routes answer 404, its RPCs FAILED_PRECONDITION, its MCP tools
// leave tools/list, its GraphQL fields fail. The data stays.

// Feature names.
const (
	FeatureChat            = "chat"
	FeatureChatExternalMCP = "chatExternalMcp"
	FeatureGeo             = "geo"
	FeatureTemporal        = "temporal"
	FeatureSpell           = "spell"
	FeatureCuration        = "curation"
	FeatureAutomation      = "automation"
	FeatureMemory          = "memory"
	FeatureCodeIntel       = "codeIntel"
	FeatureEval            = "eval"
	FeatureCorpus          = "corpus"
	FeatureClassify        = "classify"
	FeatureDuplicates      = "duplicates"
	FeatureImages          = "images"
)

// featuresForceOffEnv names the operator's override: a comma-separated list
// of modules that stay off whatever the panel says.
const featuresForceOffEnv = "MDDB_FEATURES_FORCE_OFF"

// featureDef is one module of the registry.
type featureDef struct {
	Name        string
	Description string
	Requires    []string
	Default     bool
}

// featureRegistry lists every switchable module, in the order the panel
// shows them. Everything but chatExternalMcp is on by default, so a server
// that never touched a switch behaves as before 2.19.0.
var featureRegistry = []featureDef{
	{Name: FeatureChat, Default: true, Description: "Chat assistants: scenarios, conversations and skills, and the mddb-chat service's join"},
	{Name: FeatureChatExternalMCP, Requires: []string{FeatureChat}, Description: "Lets chat assistants call tools of external MCP servers (MCP-005)"},
	{Name: FeatureGeo, Default: true, Description: "Geo search: radius, bounding box and polygon queries, geohash and the geo index"},
	{Name: FeatureTemporal, Default: true, Description: "Temporal analytics: document events, hot documents and histograms"},
	{Name: FeatureSpell, Default: true, Description: "Spell correction: suggestions, cleanup and per-collection dictionaries"},
	{Name: FeatureCuration, Default: true, Description: "Curation rules: pinned and hidden results per query"},
	{Name: FeatureAutomation, Default: true, Description: "Automation: triggers, crons and webhooks run by the server, and their logs"},
	{Name: FeatureMemory, Default: true, Description: "Agent memory: sessions, messages, recall and summaries"},
	{Name: FeatureCodeIntel, Default: true, Description: "Code intelligence: symbols, the code graph and the reference tools"},
	{Name: FeatureEval, Default: true, Description: "Retrieval evaluation: a collection's evaluation queries and their scores"},
	{Name: FeatureCorpus, Default: true, Description: "Corpus packs: read-only collections built once and installed anywhere"},
	{Name: FeatureClassify, Default: true, Description: "Classification: rank labels against a document or a text"},
	{Name: FeatureDuplicates, Default: true, Description: "Duplicate detection: identical, similar and MinHash groups"},
	{Name: FeatureImages, Default: true, Description: "Image documents: the bytes of an image document over /v1/image"},
}

// featureByName finds a module of the registry.
func featureByName(name string) (featureDef, bool) {
	for _, d := range featureRegistry {
		if d.Name == name {
			return d, true
		}
	}
	return featureDef{}, false
}

// featuresForcedOff reads MDDB_FEATURES_FORCE_OFF. Names match without
// regard to case; an unknown name switches nothing and is ignored.
func featuresForcedOff() map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.Split(os.Getenv(featuresForceOffEnv), ",") {
		n = strings.TrimSpace(n)
		for _, d := range featureRegistry {
			if strings.EqualFold(d.Name, n) {
				out[d.Name] = true
			}
		}
	}
	return out
}
