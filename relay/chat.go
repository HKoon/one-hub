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
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

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

// removeThinkTags 移除内容中的 <think>...</think> 标签及其内容
func removeThinkTags(content string) string {
	// 使用正则表达式匹配 <think>...</think> 标签（支持多行）
	re := regexp.MustCompile(`(?s)<think>.*?</think>`)
	return strings.TrimSpace(re.ReplaceAllString(content, ""))
}

// filterThinkTagsFromResponse 过滤响应中的think标签
func (r *relayChat) filterThinkTagsFromResponse(response *types.ChatCompletionResponse) {
	if response == nil {
		return
	}

	for i := range response.Choices {
		if response.Choices[i].Message.Content != nil {
			content := *response.Choices[i].Message.Content
			filteredContent := removeThinkTags(content)
			response.Choices[i].Message.Content = &filteredContent
		}
	}
}

// filterThinkTagsFromStreamResponse 过滤流式响应中的think标签
func (r *relayChat) filterThinkTagsFromStreamResponse(response *types.ChatCompletionStreamResponse) {
	if response == nil {
		return
	}

	for i := range response.Choices {
		if response.Choices[i].Delta.Content != nil {
			content := *response.Choices[i].Delta.Content
			filteredContent := removeThinkTags(content)
			response.Choices[i].Delta.Content = &filteredContent
		}
	}
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
		firstResponseTime, err = r.responseStreamClientWithFilter(response, doneStr)
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

		// 过滤响应内容中的think标签
		r.filterThinkTagsFromResponse(response)
		err = responseJsonClient(r.c, response)
	}

	if err != nil {
		done = true
	}

	return
}

// responseStreamClientWithFilter 处理流式响应并过滤think标签
func (r *relayChat) responseStreamClientWithFilter(response requester.StreamReaderInterface[string], doneStr func() string) (time.Time, *types.OpenAIErrorWithStatusCode) {
	var firstResponseTime time.Time
	var buffer strings.Builder
	var inThinkTag bool
	var thinkTagDepth int

	for {
		chunk, err := response.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return firstResponseTime, common.StringErrorWrapperLocal(err.Error(), "stream_error", http.StatusInternalServerError)
		}

		if firstResponseTime.IsZero() {
			firstResponseTime = time.Now()
		}

		// 解析流式响应
		lines := strings.Split(chunk, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || line == "data: [DONE]" {
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var streamResponse types.ChatCompletionStreamResponse
				if err := json.Unmarshal([]byte(data), &streamResponse); err != nil {
					continue
				}

				// 处理流式内容的think标签过滤
				for i := range streamResponse.Choices {
					if streamResponse.Choices[i].Delta.Content != nil {
						content := *streamResponse.Choices[i].Delta.Content
						filteredContent := r.filterStreamContent(content, &buffer, &inThinkTag, &thinkTagDepth)
						
						if filteredContent != "" {
							streamResponse.Choices[i].Delta.Content = &filteredContent
							// 重新编码并发送
							responseData, _ := json.Marshal(streamResponse)
							r.c.Writer.WriteString("data: " + string(responseData) + "\n\n")
							r.c.Writer.Flush()
						}
					} else {
						// 非内容数据直接发送
						responseData, _ := json.Marshal(streamResponse)
						r.c.Writer.WriteString("data: " + string(responseData) + "\n\n")
						r.c.Writer.Flush()
					}
				}
			}
		}
	}

	// 发送完成信号
	if doneStr != "" {
		usageData := doneStr()
		if usageData != "" {
			r.c.Writer.WriteString("data: " + usageData + "\n\n")
		}
	}
	r.c.Writer.WriteString("data: [DONE]\n\n")
	r.c.Writer.Flush()

	return firstResponseTime, nil
}

// filterStreamContent 过滤流式内容中的think标签
func (r *relayChat) filterStreamContent(content string, buffer *strings.Builder, inThinkTag *bool, thinkTagDepth *int) string {
	buffer.WriteString(content)
	fullContent := buffer.String()
	
	var result strings.Builder
	i := 0
	
	for i < len(fullContent) {
		if *inThinkTag {
			// 在think标签内，寻找结束标签
			if i+8 <= len(fullContent) && fullContent[i:i+8] == "</think>" {
				*thinkTagDepth--
				if *thinkTagDepth <= 0 {
					*inThinkTag = false
					*thinkTagDepth = 0
				}
				i += 8
				continue
			} else if i+7 <= len(fullContent) && fullContent[i:i+7] == "<think>" {
				*thinkTagDepth++
				i += 7
				continue
			}
			i++
		} else {
			// 不在think标签内，寻找开始标签
			if i+7 <= len(fullContent) && fullContent[i:i+7] == "<think>" {
				*inThinkTag = true
				*thinkTagDepth = 1
				i += 7
				continue
			} else {
				result.WriteByte(fullContent[i])
				i++
			}
		}
	}
	
	// 更新buffer为处理后的内容
	buffer.Reset()
	if *inThinkTag {
		// 如果还在think标签内，保留当前内容用于下次处理
		buffer.WriteString(fullContent)
	}
	
	return result.String()
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
