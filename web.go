package main

import webasset "cpa-balance-pilot/web"

func indexHTML() []byte {
	return webasset.IndexHTML(webasset.RuntimeConfig{
		StatePath:     statePath,
		ProvidersPath: providersPath,
		RefreshPath:   refreshPath,
	})
}
