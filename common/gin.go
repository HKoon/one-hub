package common

import (
	"bytes"
	"fmt"
	"io"
	"one-api/common/config"
	"one-api/common/logger"
	"one-api/types"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

func UnmarshalBodyReusable(c *gin.Context, v any) error {
	requestBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	err = c.Request.Body.Close()
	if err != nil {
		return err
	}
	c.Set(config.GinRequestBodyKey, requestBody)

	c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	err = c.ShouldBind(v)
	if err != nil {
		if errs, ok := err.(validator.ValidationErrors); ok {
			// 返回第一个错误字段的名称
			return fmt.Errorf("field %s is required", errs[0].Field())
		}
		return err
	}

	// c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	return nil
}

func ErrorWrapper(err error, code string, statusCode int) *types.OpenAIErrorWithStatusCode {
	errString := "error"
	if err != nil {
		errString = err.Error()
	}

	// 检测各种网络连接错误
	if strings.Contains(errString, "Post") ||
		strings.Contains(errString, "dial") ||
		strings.Contains(errString, "connection reset by peer") ||
		strings.Contains(errString, "connection refused") ||
		strings.Contains(errString, "timeout") ||
		strings.Contains(errString, "EOF") ||
		strings.Contains(errString, "broken pipe") {
		logger.SysError(fmt.Sprintf("network error: %s", errString))
		errString = "请求上游地址失败"
	}

	return StringErrorWrapper(errString, code, statusCode)
}

func ErrorWrapperLocal(err error, code string, statusCode int) *types.OpenAIErrorWithStatusCode {
	openaiErr := ErrorWrapper(err, code, statusCode)

	// 检查是否为网络连接错误，这类错误不应该标记为LocalError以便重试
	errString := ""
	if err != nil {
		errString = err.Error()
	}

	isNetworkError := strings.Contains(errString, "Post") ||
		strings.Contains(errString, "dial") ||
		strings.Contains(errString, "connection reset by peer") ||
		strings.Contains(errString, "connection refused") ||
		strings.Contains(errString, "timeout") ||
		strings.Contains(errString, "EOF") ||
		strings.Contains(errString, "broken pipe")

	// 只有非网络错误才标记为LocalError
	if !isNetworkError {
		openaiErr.LocalError = true
	}

	return openaiErr
}

func ErrorToOpenAIError(err error) *types.OpenAIError {
	return &types.OpenAIError{
		Code:    "system error",
		Message: err.Error(),
		Type:    "one_hub_error",
	}
}

func StringErrorWrapper(err string, code string, statusCode int) *types.OpenAIErrorWithStatusCode {
	openAIError := types.OpenAIError{
		Message: err,
		Type:    "one_hub_error",
		Code:    code,
	}
	return &types.OpenAIErrorWithStatusCode{
		OpenAIError: openAIError,
		StatusCode:  statusCode,
	}
}

func StringErrorWrapperLocal(err string, code string, statusCode int) *types.OpenAIErrorWithStatusCode {
	openaiErr := StringErrorWrapper(err, code, statusCode)
	openaiErr.LocalError = true
	return openaiErr

}

func AbortWithMessage(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "one_hub_error",
		},
	})
	c.Abort()
	logger.LogError(c.Request.Context(), message)
}

func AbortWithErr(c *gin.Context, statusCode int, err error) {
	c.JSON(statusCode, err)
	c.Abort()
	logger.LogError(c.Request.Context(), err.Error())
}

func APIRespondWithError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{
		"success": false,
		"message": err.Error(),
	})
}

func StringRerankErrorWrapper(err string, code string, statusCode int) *types.RerankErrorWithStatusCode {
	rerankError := types.RerankError{
		Detail: err,
	}
	return &types.RerankErrorWithStatusCode{
		RerankError: rerankError,
		StatusCode:  statusCode,
	}
}

func StringRerankErrorWrapperLocal(err string, code string, statusCode int) *types.RerankErrorWithStatusCode {
	rerankError := StringRerankErrorWrapper(err, code, statusCode)
	rerankError.LocalError = true
	return rerankError

}

func OpenAIErrorToRerankError(err *types.OpenAIErrorWithStatusCode) *types.RerankErrorWithStatusCode {
	return &types.RerankErrorWithStatusCode{
		RerankError: types.RerankError{
			Detail: err.Message,
		},
		StatusCode: err.StatusCode,
		LocalError: err.LocalError,
	}
}
