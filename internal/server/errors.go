package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
)

type ApiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ApiError) Error() string {
	return fmt.Sprintf("code: %d message: %s", e.Code, e.Message)
}

func newError(code int, message string) *ApiError {
	return &ApiError{Code: code, Message: message}
}

func NotFoundError(message string) *ApiError {
	return newError(http.StatusNotFound, message)
}

func BadRequestError(message string) *ApiError {
	return newError(http.StatusBadRequest, message)
}

func InternalError(message string) *ApiError {
	return newError(http.StatusInternalServerError, message)
}

func ServiceUnavailableError(message string) *ApiError {
	return newError(http.StatusServiceUnavailable, message)
}

// ResolveError maps any error to an ApiError: ApiErrors pass through,
// sql.ErrNoRows becomes 404, anything else 500.
func ResolveError(err error) *ApiError {
	var apiErr *ApiError
	switch {
	case errors.As(err, &apiErr):
		return apiErr
	case errors.Is(err, sql.ErrNoRows):
		return NotFoundError(err.Error())
	default:
		return InternalError(err.Error())
	}
}
