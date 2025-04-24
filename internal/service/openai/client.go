package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"jira_whisperer/internal/logger"

	"github.com/Azure/azure-sdk-for-go/sdk/ai/azopenai"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"go.uber.org/zap"
)

type Client struct {
	client         *azopenai.Client
	deploymentName string
}

func NewClient(endpoint, apiKey, deploymentName string) (*Client, error) {
	keyCredential := azcore.NewKeyCredential(apiKey)
	client, err := azopenai.NewClientWithKeyCredential(endpoint, keyCredential, nil)
	if err != nil {
		return nil, err
	}

	return &Client{
		client:         client,
		deploymentName: deploymentName,
	}, nil
}

func (c *Client) Chat(ctx context.Context, messages []azopenai.ChatRequestMessageClassification) (string, error) {
	resp, err := c.client.GetChatCompletions(ctx, azopenai.ChatCompletionsOptions{
		DeploymentName: to.Ptr(c.deploymentName),
		Messages:       messages,
		N:              to.Ptr[int32](1),
	}, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return *resp.Choices[0].Message.Content, nil
}

type Tool struct {
	Name        string
	Description string
	Parameters  string
}

// ToolCall 表示一个工具调用的结果
type ToolCall struct {
	ID       string                 // 工具调用的唯一标识符
	Name     string                 // 工具名称
	Args     map[string]interface{} // 工具参数
	Response interface{}            // 工具调用的响应
}

// ChatResponse 表示一次对话的完整响应
type ChatResponse struct {
	Content    string     // 文本内容
	ToolCalls  []ToolCall // 工具调用列表
	IsComplete bool       // 是否完成（不需要进一步的工具调用）
}

func (c *Client) ChatWithTools(ctx context.Context, messages []azopenai.ChatRequestMessageClassification, tools []Tool) (*ChatResponse, error) {
	// 转换工具为 Azure OpenAI 格式
	var azureTools []azopenai.FunctionDefinition
	for _, tool := range tools {
		azureTools = append(azureTools, azopenai.FunctionDefinition{
			Name:        to.Ptr(tool.Name),
			Description: to.Ptr(tool.Description),
			Parameters:  []byte(tool.Parameters),
		})
	}

	// log message send to AI
	logger.GetLogger().Warn("sending messages to AI", zap.Any("messages", messages))
	resp, err := c.client.GetChatCompletions(ctx, azopenai.ChatCompletionsOptions{
		DeploymentName: to.Ptr(c.deploymentName),
		Messages:       messages,
		N:              to.Ptr[int32](1),
		Functions:      azureTools,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get chat completion: %v", err)
	}
	logger.GetLogger().Warn("chat completion response", zap.Any("response", resp))

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from chat completion")
	}

	choice := resp.Choices[0]
	response := &ChatResponse{
		IsComplete: true, // 默认为完成
	}

	// 处理工具调用
	if choice.Message.FunctionCall != nil || len(choice.Message.ToolCalls) > 0 {
		response.IsComplete = false // 有工具调用，标记为未完成

		// 处理旧版 FunctionCall
		if choice.Message.FunctionCall != nil {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(*choice.Message.FunctionCall.Arguments), &args); err != nil {
				return nil, fmt.Errorf("failed to parse function arguments: %v", err)
			}
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				Name: *choice.Message.FunctionCall.Name,
				Args: args,
			})
		}

		// 处理新版 ToolCalls
		for _, call := range choice.Message.ToolCalls {
			switch v := call.(type) {
			case *azopenai.ChatCompletionsFunctionToolCall:
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(*v.Function.Arguments), &args); err != nil {
					return nil, fmt.Errorf("failed to parse tool arguments: %v", err)
				}
				response.ToolCalls = append(response.ToolCalls, ToolCall{
					ID:   *v.ID,
					Name: *v.Function.Name,
					Args: args,
				})
			// if not match, railse error
			default:
				logger.GetLogger().Error("unknown tool call", zap.Any("tool", v))
			}
		}
	}

	if choice.Message.Content != nil {
		response.Content = *choice.Message.Content
	}

	return response, nil
}
