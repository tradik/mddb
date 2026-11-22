package main

import (
	"os"
	"strings"

	"mddb/internal/envconf"
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
	FeatureWebhooks        = "webhooks"
	FeatureSchemas         = "schemas"
	FeatureAliases         = "aliases"
	FeatureCrossSearch     = "crossSearch"
	FeatureSearchTuning    = "searchTuning"
	FeatureFTSTuning       = "ftsTuning"
	FeatureWebSearch       = "webSearch"
	FeatureRerankAPI       = "rerankApi"
	FeatureExtractAPI      = "extractApi"
	FeatureVectorStores    = "vectorStores"
	FeatureESCompat        = "esCompat"
	FeatureQdrantCompat    = "qdrantCompat"
	FeatureModelTraining   = "modelTraining"
)

// featuresForceOffEnv names the operator's override: a comma-separated list
// of modules that stay off whatever the panel says.
const featuresForceOffEnv = "MDDB_FEATURES_FORCE_OFF"

// featureDef is one module of the registry.
type featureDef struct {
	Name string
	// Title and Summary are for the person switching the module: a short
	// name and one plain sentence. Description is the technical detail.
	Title       string
	Summary     string
	Description string
	Requires    []string
	Default     bool
	// DefaultEnv names an older switch that, set to "true", turns the module
	// on by default (MDDB_ES_COMPAT, MDDB_QDRANT_COMPAT): a server that used
	// it keeps the surface after the upgrade. A switch set on the Features
	// page wins over it.
	DefaultEnv string
}

// defaultOn is the module's state while no switch is stored.
func (d featureDef) defaultOn() bool {
	return d.Default || (d.DefaultEnv != "" && envconf.String(d.DefaultEnv, "false") == "true")
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
