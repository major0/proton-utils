package lumo

// OAIMessage is a single message in an OpenAI-compatible chat conversation.
// Named OAIMessage to avoid conflict with the Lumo Message type.
type OAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionRequest is the request body for /v1/chat/completions.
type ChatCompletionRequest struct {
	Model       string       `json:"model"`
	Messages    []OAIMessage `json:"messages"`
	Stream      bool         `json:"stream,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	MaxTokens   *int         `json:"max_tokens,omitempty"`
}

// ChatCompletionResponse is the non-streaming response body.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is a single completion choice.
type Choice struct {
	Index        int         `json:"index"`
	Message      *OAIMessage `json:"message,omitempty"`
	Delta        *OAIMessage `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

// Usage reports token counts.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionChunk is a single SSE chunk in a streaming response.
type ChatCompletionChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// OAIModel is a single model entry in the models list.
// Named OAIModel to clarify its OpenAI-compatibility role.
type OAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// OAIModelList is the response body for GET /v1/models.
type OAIModelList struct {
	Object string     `json:"object"`
	Data   []OAIModel `json:"data"`
}

// OAIErrorBody is the inner error object in an error response.
type OAIErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    *string `json:"code,omitempty"`
}

// OAIErrorResponse is the OpenAI-format error response body.
type OAIErrorResponse struct {
	Error OAIErrorBody `json:"error"`
}
