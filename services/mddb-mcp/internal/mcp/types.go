package mcp

// Resource reprezentuje zasób MCP.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// ResourceReadRequest reprezentuje żądanie odczytu zasobu.
type ResourceReadRequest struct {
	URI string `json:"uri"`
}

// Tool reprezentuje narzędzie MCP.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolCallRequest reprezentuje żądanie wywołania narzędzia.
type ToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}
