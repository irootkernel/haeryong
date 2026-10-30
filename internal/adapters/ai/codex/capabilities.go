package codex

import (
	"context"
	"encoding/json"
	"haeryong.local/haeryong/internal/domain"
	"time"
)

func inheritedCapabilities(ctx context.Context, t *transport, cwd, thread string) domain.ProviderCapabilities {
	c := domain.ProviderCapabilities{WebSearch: "unknown", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	var config struct {
		Config *struct {
			WebSearch *string `json:"web_search"`
		} `json:"config"`
	}
	if t.call(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}, &config) == nil && config.Config != nil && config.Config.WebSearch != nil {
		switch *config.Config.WebSearch {
		case "disabled", "cached", "live":
			c.WebSearch = *config.Config.WebSearch
		}
	}
	connected, tools := 0, 0
	cursor := ""
	seen := map[string]bool{}
	for {
		var page struct {
			Data *[]struct {
				Runtime    *string                     `json:"runtimeStatus"`
				Tools      *map[string]json.RawMessage `json:"tools"`
				ToolsError *string                     `json:"toolsError"`
			} `json:"data"`
			Next *string `json:"nextCursor"`
		}
		params := map[string]any{"threadId": thread, "detail": "toolsAndAuthOnly"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if t.call(ctx, "mcpServerStatus/list", params, &page) != nil || page.Data == nil {
			return c
		}
		for _, server := range *page.Data {
			if server.Runtime == nil {
				return c
			}
			switch *server.Runtime {
			case "notStarted", "starting", "connected", "authenticationRequired", "failed", "cancelled", "disabled":
			default:
				return c
			}
			if *server.Runtime == "connected" {
				if server.ToolsError != nil || server.Tools == nil {
					return c
				}
				connected++
				tools += len(*server.Tools)
			}
		}
		if page.Next == nil || *page.Next == "" {
			c.MCPConnected = &connected
			c.MCPTools = &tools
			c.MCPComplete = true
			return c
		}
		if seen[*page.Next] {
			return c
		}
		seen[*page.Next] = true
		cursor = *page.Next
	}
}
