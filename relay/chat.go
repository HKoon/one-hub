package relay

import (
	"os"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"one-api/common"
	"one-api/common/requester"
	"one-api/common/utils"
	"log"
	providersBase "one-api/providers/base"
	"one-api/types"
	"sync"

	"github.com/gin-gonic/gin"
)

type relayChat struct {
	relayBase
	chatRequest types.ChatCompletionRequest
}

func NewRelayChat(c *gin.Context) *relayChat {
	relay := &relayChat{}
	relay.c = c
	return relay
}

// Config struct 用于解析 JSON 配置
type ChatConfig struct {
	Groups   map[string]GroupConfig `json:"groups"`
	Models   map[string]string      `json:"models"`
	Defaults GroupConfig            `json:"defaults"`
}

// GroupConfig 定义了 preprompt 和 guideline 的结构
type GroupConfig struct {
	Preprompt string `json:"preprompt"`
	Guideline string `json:"guideline"`
}

// 全局缓存和同步控制
var (
	chatConfig ChatConfig
	configOnce sync.Once
	cacheMutex sync.RWMutex
)

// Lazy load and parse JSON config from environment variable
func loadConfig() error {
	configOnce.Do(func() {
		// 获取环境变量中的 JSON 配置
		configData := os.Getenv("CHAT_CONFIG")
		if configData == "" {
			fmt.Println("CHAT_CONFIG 环境变量未设置")
			return
		}

		// 解析 JSON 配置
		err := json.Unmarshal([]byte(configData), &chatConfig)
		if err != nil {
			fmt.Println("解析 JSON 配置失败:", err)
		}
	})

	return nil
}

// 根据模型名称获取 preprompt 和 guideline
func getPrepromptAndGuidelineCached(modelName string) (string, string) {
	// 加载配置 (lazy load)
	if err := loadConfig(); err != nil {
		fmt.Println("加载配置失败:", err)
		return "", ""
	}

	// 查找模型对应的组
	cacheMutex.RLock()
	groupName, modelExists := chatConfig.Models[modelName]
	cacheMutex.RUnlock()

	// 如果模型存在，获取该组的 preprompt 和 guideline
	if modelExists {
		cacheMutex.RLock()
		groupConfig, groupExists := chatConfig.Groups[groupName]
		cacheMutex.RUnlock()

		if groupExists {
			return groupConfig.Preprompt, groupConfig.Guideline
		}
	}

	// 如果没有找到模型或组，返回默认的 preprompt 和 guideline
	return chatConfig.Defaults.Preprompt, chatConfig.Defaults.Guideline
}

func (r *relayChat) setRequest() error {
	if err := common.UnmarshalBodyReusable(r.c, &r.chatRequest); err != nil {
		return err
	}

	if r.chatRequest.MaxTokens < 0 || r.chatRequest.MaxTokens > math.MaxInt32/2 {
		return errors.New("max_tokens is invalid")
	}

	if r.chatRequest.Tools != nil {
		r.c.Set("skip_only_chat", true)
	}

	if !r.chatRequest.Stream && r.chatRequest.StreamOptions != nil {
		return errors.New("the 'stream_options' parameter is only allowed when 'stream' is enabled")
	}

	r.originalModel = r.chatRequest.Model

	log.Printf("Before preprocessing: %+v\n", r.chatRequest)
	// Add preprocessing step
	if err := r.preprocessMessages(); err != nil {
		return err
	}
	
	log.Printf("After preprocessing: %+v\n", r.chatRequest)

	return nil
}

func (r *relayChat) preprocessMessages() error {
	model := r.chatRequest.Model

	// 根据模型名称获取 preprompt 和 guideline
	preprompt, guideline := getPrepromptAndGuidelineCached(model)
	log.Printf("Preprompt: %s, Guideline: %s\n", preprompt, guideline)

	// 查找唯一的 system 消息并进行处理
	for i, message := range r.chatRequest.Messages {
		if message.Role == "system" {
			content, ok := message.Content.(string)
			if !ok {
				return errors.New("系统消息的内容不是字符串类型")
			}
			// 为 system 消息添加 preprompt 和 guideline
			r.chatRequest.Messages[i].Content = preprompt + content + guideline
			break // 找到 system 消息后立即退出循环
		}
	}

	// 如果没有找到 system 消息，可以选择添加新的系统消息
	/*
	newSystemMessage := ChatCompletionMessage{
		Role:    "system",
		Content: preprompt,
	}
	r.chatRequest.Messages = append([]ChatCompletionMessage{newSystemMessage}, r.chatRequest.Messages...)
	*/

	return nil
}

func (r *relayChat) getRequest() interface{} {
	return &r.chatRequest
}

func (r *relayChat) IsStream() bool {
	return r.chatRequest.Stream
}

func (r *relayChat) getPromptTokens() (int, error) {
	channel := r.provider.GetChannel()
	return common.CountTokenMessages(r.chatRequest.Messages, r.modelName, channel.PreCost), nil
}

func (r *relayChat) send() (err *types.OpenAIErrorWithStatusCode, done bool) {
	chatProvider, ok := r.provider.(providersBase.ChatInterface)
	if !ok {
		err = common.StringErrorWrapperLocal("channel not implemented", "channel_error", http.StatusServiceUnavailable)
		done = true
		return
	}

	r.chatRequest.Model = r.modelName

	if r.chatRequest.Stream {
		var response requester.StreamReaderInterface[string]
		response, err = chatProvider.CreateChatCompletionStream(&r.chatRequest)
		if err != nil {
			return
		}

		doneStr := func() string {
			return r.getUsageResponse()
		}

		// 包装 StreamReaderInterface 以修改模型名称
		wrappedResponse := &modelNameWrapper{
			StreamReaderInterface: response,
			originalModel:         r.originalModel,
		}

		err = responseStreamClient(r.c, wrappedResponse, r.cache, doneStr)
	} else {
		var response *types.ChatCompletionResponse
		response, err = chatProvider.CreateChatCompletion(&r.chatRequest)
		if err != nil {
			return
		}

		// 修改响应中的模型名称
		response.Model = r.originalModel

		err = responseJsonClient(r.c, response)

		if err == nil && response.GetContent() != "" {
			r.cache.SetResponse(response)
		}
	}

	if err != nil {
		done = true
	}

	return
}

func (r *relayChat) getUsageResponse() string {
	if r.chatRequest.StreamOptions != nil && r.chatRequest.StreamOptions.IncludeUsage {
		usageResponse := types.ChatCompletionStreamResponse{
			ID:      fmt.Sprintf("chatcmpl-%s", utils.GetUUID()),
			Object:  "chat.completion.chunk",
			Created: utils.GetTimestamp(),
			Model:   r.originalModel,
			Choices: []types.ChatCompletionStreamChoice{},
			Usage:   r.provider.GetUsage(),
		}

		responseBody, err := json.Marshal(usageResponse)
		if err != nil {
			return ""
		}

		return string(responseBody)
	}

	return ""
}
