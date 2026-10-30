package main

import "context"

// Corpus packs through the direct client (#378): the functions the REST
// handlers call, so every surface lists, installs and removes alike.

// CorpusList returns the packs installed in the caller's tenant.
func (c *DirectClient) CorpusList(ctx context.Context) ([]corpusListEntry, error) {
	return c.server.corpusPacks(ctx), nil
}

// CorpusInstall downloads the pack at an https URL and installs it.
func (c *DirectClient) CorpusInstall(ctx context.Context, url, sha256 string) (*corpusInstallResult, error) {
	return c.server.installCorpusFromURL(ctx, url, sha256)
}

// CorpusRemove removes an installed pack and returns the documents deleted.
func (c *DirectClient) CorpusRemove(_ context.Context, name string) (int, error) {
	return c.server.removeCorpus(name)
}
