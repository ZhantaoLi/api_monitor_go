package sqlite

// DetectionResult is the storage-layer shape used when persisting model checks.
type DetectionResult struct {
	Protocol         string  `json:"protocol"`
	Model            string  `json:"model"`
	Stream           bool    `json:"stream"`
	Duration         float64 `json:"duration"`
	TTFB             float64 `json:"ttfb"`
	Ping             float64 `json:"ping"`
	Success          bool    `json:"success"`
	TransportSuccess bool    `json:"transport_success"`
	ToolCallsCount   int     `json:"tool_calls_count"`
	ToolCalls        string  `json:"tool_calls"`
	Content          string  `json:"content"`
	Timestamp        float64 `json:"timestamp"`
	Error            *string `json:"error"`
	StatusCode       *int    `json:"status_code"`
	Route            string  `json:"route"`
	Endpoint         string  `json:"endpoint"`
}
