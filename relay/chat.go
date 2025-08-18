package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	thinkTagFilter *thinkTagStreamFilter
}

// thinkTagStreamFilter 用于过滤流式输出中的think标签
type thinkTagStreamFilter struct {
	buffer     strings.Builder
	inThinkTag bool
	tagDepth   int
}

// newThinkTagStreamFilter 创建新的think标签过滤器
func newThinkTagStreamFilter() *thinkTagStreamFilter {
	return &thinkTagStreamFilter{
		buffer:     strings.Builder{},
		inThinkTag: false,
		tagDepth:   0,
	}
}

// Filter 过滤流式数据中的think标签
func (f *thinkTagStreamFilter) Filter(data string) string {
	f.buffer.WriteString(data)
	content := f.buffer.String()
	
	// 使用正则表达式处理think标签
	for {
		if !f.inThinkTag {
			// 查找开始标签
			openIndex := strings.Index(content, "<think>")
			if openIndex == -1 {
				// 没有找到开始标签，返回所有内容
				f.buffer.Reset()
				return content
			}
			
			// 找到开始标签，保留标签前的内容
			result := content[:openIndex]
			f.inThinkTag = true
			content = content[openIndex+7:] // 跳过"<think>"
			f.buffer.Reset()
			f.buffer.WriteString(content)
			return result
		} else {
			// 在think标签内，查找结束标签
			closeIndex := strings.Index(content, "</think>")
			if closeIndex == -1 {
				// 没有找到结束标签，丢弃所有内容
				f.buffer.Reset()
				return ""
			}
			
			// 找到结束标签，跳过标签内容和结束标签
			f.inThinkTag = false
			content = content[closeIndex+8:] // 跳过"</think>"
			f.buffer.Reset()
			f.buffer.WriteString(content)
			// 继续处理剩余内容
		}
	}
}

func NewRelayChat(c *gin.Context) *relayChat {
	relay := &relayChat{
		relayBase: relayBase{
			allowHeartbeat: true,
			c:              c,
		},
		thinkTagFilter: newThinkTagStreamFilter(),
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
		// 根据实际类型处理Content字段
		if content, ok := response.Choices[i].Message.Content.(string); ok {
			filteredContent := removeThinkTags(content)
			response.Choices[i].Message.Content = filteredContent
		}
	}
}

var need2Response = map[string]bool{
	"o3-pro-2025-06-10":                true,
	"o3-pro":                           true,
	"o1-pro-2025-03-19":                true,
	"o1-pro":                           true,
	"o3-deep-research-2025-06-26":      true,
	"o3-deep-research":                 true,
	"o4-mini-deep-research-2025-06-26": true,
	"o4-mini-deep-research":            true,
	"codex-mini-latest":                true,
}

func (r *relayChat) send() (err *types.OpenAIErrorWithStatusCode, done bool) {
	if need2Response[r.modelName] {
		resProvider, ok := r.provider.(providersBase.ResponsesInterface)
		if ok {
			return r.compatibleSend(resProvider)
		}
	}

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
    
    // 修改响应中的模型名称
		response.Model = r.originalModel
		
    // 过滤响应内容中的think标签
		r.filterThinkTagsFromResponse(response)
		err = responseJsonClient(r.c, response)
	}

	if err != nil {
		done = true
	}

	return
}

// responseStreamClientWithFilter 带有think标签过滤的流式响应处理
func (r *relayChat) responseStreamClientWithFilter(c *gin.Context, stream requester.StreamReaderInterface[string], endHandler func() string) (firstResponseTime time.Time, errWithOP *types.OpenAIErrorWithStatusCode) {
	requester.SetEventStreamHeaders(c)
	dataChan, errChan := stream.Recv()

	// 创建一个done channel用于通知处理完成
	done := make(chan struct{})
	var finalErr *types.OpenAIErrorWithStatusCode

	defer stream.Close()

	var isFirstResponse bool

	// 在新的goroutine中处理stream数据
	go func() {
		defer close(done)

		for {
			select {
			case data, ok := <-dataChan:
				if !ok {
					return
				}
				
				// 使用think标签过滤器处理数据
				filteredData := r.thinkTagFilter.Filter(data)
				
				// 只有当过滤后的数据不为空时才发送
				if filteredData != "" {
					streamData := "data: " + filteredData + "\n\n"

					if !isFirstResponse {
						firstResponseTime = time.Now()
						isFirstResponse = true
					}

					// 尝试写入数据，如果客户端断开也继续处理
					select {
					case <-c.Request.Context().Done():
						// 客户端已断开，不执行任何操作，直接跳过
					default:
						// 客户端正常，发送数据
						c.Writer.Write([]byte(streamData))
						c.Writer.Flush()
					}
				}

			case err := <-errChan:
				if !errors.Is(err, io.EOF) {
					// 处理错误情况
					errMsg := "data: " + err.Error() + "\n\n"
					select {
					case <-c.Request.Context().Done():
						// 客户端已断开，不执行任何操作，直接跳过
					default:
						// 客户端正常，发送错误信息
						c.Writer.Write([]byte(errMsg))
						c.Writer.Flush()
					}

					finalErr = common.StringErrorWrapper(err.Error(), "stream_error", 900)
				} else {
					// 正常结束，处理endHandler
					if finalErr == nil && endHandler != nil {
						streamData := endHandler()
						if streamData != "" {
							select {
							case <-c.Request.Context().Done():
								// 客户端已断开，不执行任何操作，直接跳过
							default:
								// 客户端正常，发送数据
								c.Writer.Write([]byte("data: " + streamData + "\n\n"))
								c.Writer.Flush()
							}
						}
					}

					// 发送结束标记
					streamData := "data: [DONE]\n\n"
					select {
					case <-c.Request.Context().Done():
						// 客户端已断开，不执行任何操作，直接跳过
					default:
						// 客户端正常，发送数据
						c.Writer.Write([]byte(streamData))
						c.Writer.Flush()
					}
				}
				return
			}
		}
	}()

	// 等待处理完成
	<-done

	errWithOP = finalErr
	return firstResponseTime, errWithOP
}

func (r *relayChat) getUsageResponse() string {
	if r.chatRequest.StreamOptions != nil && r.chatRequest.StreamOptions.IncludeUsage {
		usageResponse := types.ChatCompletionStreamResponse{
			ID:      fmt.Sprintf("chatcmpl-%s", utils.GetUUID()),
			Object:  "chat.completion.chunk",
			Created: utils.GetTimestamp(),
			Model:   r.originalModel, //r.chatRequest.Model,
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

func (r *relayChat) compatibleSend(resProvider providersBase.ResponsesInterface) (err *types.OpenAIErrorWithStatusCode, done bool) {
	resRequest := r.chatRequest.ToResponsesRequest()
	resRequest.ConvertChat = true

	if r.chatRequest.Stream {
		var response requester.StreamReaderInterface[string]
		response, err = resProvider.CreateResponsesStream(resRequest)
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
		firstResponseTime, err = r.responseStreamClientWithFilter(r.c, response, doneStr)
		r.SetFirstResponseTime(firstResponseTime)
	} else {
		var response *types.OpenAIResponsesResponses
		response, err = resProvider.CreateResponses(resRequest)
		if err != nil {
			return
		}

		if r.heartbeat != nil {
			r.heartbeat.Stop()
		}
		err = responseJsonClient(r.c, response.ToChat())
	}

	if err != nil {
		done = true
	}

	return
}
