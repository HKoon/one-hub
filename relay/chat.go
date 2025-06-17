package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"one-api/common"
	"one-api/common/config"
	"one-api/common/requester"
	"one-api/common/utils"
	providersBase "one-api/providers/base"
	"one-api/safty"
	"one-api/types"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
)

// filterThinkTags 过滤掉 <think>...</think> 标签及其内容
func filterThinkTags(content string) string {
	re := regexp.MustCompile(`(?s)<think>.*?</think>`)
	return re.ReplaceAllString(content, "")
}

type relayChat struct {
	relayBase
	chatRequest types.ChatCompletionRequest
}

func NewRelayChat(c *gin.Context) *relayChat {
	relay := &relayChat{
		relayBase: relayBase{
			allowHeartbeat: true,
			c:              c,
		},
	}
	return relay
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

	if !r.chatRequest.Stream {
		r.chatRequest.StreamOptions = nil
	}

	r.setOriginalModel(r.chatRequest.Model)

	otherArg := r.getOtherArg()

	if otherArg == "search" {
		handleSearch(r.c, &r.chatRequest)
		return nil
	}

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
	// 内容审查
	if config.EnableSafe {
		for _, message := range r.chatRequest.Messages {
			if message.Content != nil {
				CheckResult, _ := safty.CheckContent(message.Content)
				if !CheckResult.IsSafe {
					err = common.StringErrorWrapperLocal(CheckResult.Reason, CheckResult.Code, http.StatusBadRequest)
					done = true
					return
				}
			}
		}
	}

	if r.chatRequest.Stream {
		var response requester.StreamReaderInterface[string]
		response, err = chatProvider.CreateChatCompletionStream(&r.chatRequest)
		if err != nil {
			return
		}

		if r.heartbeat != nil {
			r.heartbeat.Stop()
		}

		doneStr := func() string {
			return r.getUsageResponse()
		}

		var firstResponseTime time.Time
		// 使用自定义的流式响应处理函数
		firstResponseTime, err = r.responseStreamClientWithFilter(r.c, response, doneStr)
		r.SetFirstResponseTime(firstResponseTime)
	} else {
		var response *types.ChatCompletionResponse
		response, err = chatProvider.CreateChatCompletion(&r.chatRequest)
		if err != nil {
			return
		}

		if r.heartbeat != nil {
			r.heartbeat.Stop()
		}

		// 过滤响应内容
		r.filterChatResponse(response)
		err = responseJsonClient(r.c, response)
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
			Model:   r.chatRequest.Model,
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

// filterChatResponse 过滤聊天响应中的 <think> 标签
func (r *relayChat) filterChatResponse(response *types.ChatCompletionResponse) {
	for i := range response.Choices {
		if response.Choices[i].Message.Content != nil {
			switch content := response.Choices[i].Message.Content.(type) {
			case string:
				response.Choices[i].Message.Content = filterThinkTags(content)
			case []interface{}:
				// 处理多模态内容
				for j, item := range content {
					if textItem, ok := item.(map[string]interface{}); ok {
						if textItem["type"] == "text" {
							if text, exists := textItem["text"].(string); exists {
								textItem["text"] = filterThinkTags(text)
								content[j] = textItem
							}
						}
					}
				}
				response.Choices[i].Message.Content = content
			}
		}
	}
}

// responseStreamClientWithFilter 带过滤功能的流式响应处理
func (r *relayChat) responseStreamClientWithFilter(c *gin.Context, stream requester.StreamReaderInterface[string], endHandler func() string) (firstResponseTime time.Time, errWithOP *types.OpenAIErrorWithStatusCode) {
	requester.SetEventStreamHeaders(c)
	dataChan, errChan := stream.Recv()

	done := make(chan struct{})
	var finalErr *types.OpenAIErrorWithStatusCode

	defer stream.Close()

	var isFirstResponse bool

	go func() {
		defer close(done)

		for {
			select {
			case data, ok := <-dataChan:
				if !ok {
					return
				}
				// 过滤 <think>...</think> 标签内容
				filteredData := filterThinkTags(data)
				streamData := "data: " + filteredData + "\n\n"

				if !isFirstResponse {
					firstResponseTime = time.Now()
					isFirstResponse = true
				}

				select {
				case <-c.Request.Context().Done():
					// 客户端已断开
				default:
					c.Writer.Write([]byte(streamData))
					c.Writer.Flush()
				}

			case err := <-errChan:
				// 错误处理逻辑...
				return
			}
		}
	}()

	<-done
	return firstResponseTime, finalErr
}
