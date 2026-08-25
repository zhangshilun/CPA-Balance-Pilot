// web 包嵌入自包含的 CPA Balance Pilot 管理页面。
package web

import (
	"embed"
	"encoding/json"
	"strings"
)

//go:embed index.html styles.css app.js
var assets embed.FS

// RuntimeConfig 由管理路由注入，app.js 不直接包含 API 路径。
type RuntimeConfig struct {
	StatePath, ProvidersPath, RefreshPath string
}

func IndexHTML(config RuntimeConfig) []byte {
	page, err := assets.ReadFile("index.html")
	if err != nil {
		return nil
	}
	styles, err := assets.ReadFile("styles.css")
	if err != nil {
		return nil
	}
	script, err := assets.ReadFile("app.js")
	if err != nil {
		return nil
	}
	runtime, err := json.Marshal(config)
	if err != nil {
		return nil
	}
	result := strings.ReplaceAll(string(page), `<link rel="stylesheet" href="styles.css">`, "<style>\n"+string(styles)+"\n</style>")
	result = strings.ReplaceAll(result, `<script src="app.js"></script>`, "<script>\n"+string(script)+"\n</script>")
	return []byte(strings.ReplaceAll(result, "__CPA_PLUGIN_RUNTIME_CONFIG__", string(runtime)))
}
