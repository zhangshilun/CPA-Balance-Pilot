package main

const (
	pluginName             = "CPA Balance Pilot"
	pluginMenu             = "CPA Balance Pilot"
	pluginAuthor           = "Solon.Z"
	pluginRepository       = "https://github.com/zhangshilun/CPA-Balance-Pilot"
	resourcePath           = "/open"
	dataDirectory          = "cpa-balance-pilot-data"
	defaultDataDirectory   = "/CLIProxyAPI/data/cpa-balance-pilot"
	balanceKeyConfigKey    = "CPA_BALANCE_PILOT_KEY"
	dataDirectoryConfigKey = "CPA_BALANCE_PILOT_DIR"
	pluginID               = "cpa-balance-pilot"
	pluginABIVersion       = 1
	apiPathPrefix          = "/cpa-balance-pilot"
	resourceFullPath       = "/v0/resource/plugins/cpa-balance-pilot" + resourcePath
	maxRequestBytes        = 1 << 20
	rpcSchemaVersion       = 2

	methodPluginRegister     = "plugin.register"
	methodPluginReconfigure  = "plugin.reconfigure"
	methodManagementRegister = "management.register"
	methodManagementHandle   = "management.handle"
)

var pluginVersion = "0.2.0-dev"
