package response

import (
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
)

// Response represents a standard API response
type Response struct {
	Success bool         `json:"success"`
	Message string       `json:"message,omitempty"`
	Data    interface{}  `json:"data,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
	Meta    *Meta        `json:"meta,omitempty"`
}

// ErrorDetail contains error information
type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// Meta contains pagination and additional metadata
type Meta struct {
	Page       int   `json:"page,omitempty"`
	Limit      int   `json:"limit,omitempty"`
	TotalPages int   `json:"total_pages,omitempty"`
	TotalCount int64 `json:"total_count,omitempty"`
	Total      int64 `json:"total,omitempty"` // Alias for frontend compatibility
}

// Success sends a successful response
func Success(c *gin.Context, statusCode int, message string, data interface{}) {
	c.JSON(statusCode, Response{
		Success: true,
		Message: message,
		Data:    emptyCollection(data),
	})
}

// SuccessWithMeta sends a successful response with metadata
func SuccessWithMeta(c *gin.Context, statusCode int, message string, data interface{}, meta *Meta) {
	c.JSON(statusCode, Response{
		Success: true,
		Message: message,
		Data:    emptyCollection(data),
		Meta:    meta,
	})
}

// Error sends an error response
func Error(c *gin.Context, statusCode int, code string, message string, details interface{}) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// BadRequest sends a 400 Bad Request response
func BadRequest(c *gin.Context, message string, details interface{}) {
	Error(c, http.StatusBadRequest, "BAD_REQUEST", message, details)
}

// Unauthorized sends a 401 Unauthorized response
func Unauthorized(c *gin.Context, message string) {
	Error(c, http.StatusUnauthorized, "UNAUTHORIZED", message, nil)
}

// Forbidden sends a 403 Forbidden response
func Forbidden(c *gin.Context, message string) {
	Error(c, http.StatusForbidden, "FORBIDDEN", message, nil)
}

// NotFound sends a 404 Not Found response
func NotFound(c *gin.Context, message string) {
	Error(c, http.StatusNotFound, "NOT_FOUND", message, nil)
}

// Conflict sends a 409 Conflict response
func Conflict(c *gin.Context, message string) {
	Error(c, http.StatusConflict, "CONFLICT", message, nil)
}

// InternalServerError sends a 500 Internal Server Error response
func InternalServerError(c *gin.Context, message string) {
	Error(c, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", message, nil)
}

// Created sends a 201 Created response
func Created(c *gin.Context, message string, data interface{}) {
	Success(c, http.StatusCreated, message, data)
}

// OK sends a 200 OK response
func OK(c *gin.Context, message string, data interface{}) {
	Success(c, http.StatusOK, message, data)
}

// NoContent sends a 204 No Content response
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// Pagination represents pagination parameters
type Pagination struct {
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// NewPagination creates a new Pagination with calculated total pages
func NewPagination(page, limit int, total int64) Pagination {
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages < 1 {
		totalPages = 1
	}
	return Pagination{
		Page:       page,
		Limit:      limit,
		Total:      total,
		TotalPages: totalPages,
	}
}

// SuccessWithPagination sends a successful paginated response
func SuccessWithPagination(c *gin.Context, message string, data interface{}, pagination Pagination) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Message: message,
		Data:    emptyCollection(data),
		Meta: &Meta{
			Page:       pagination.Page,
			Limit:      pagination.Limit,
			TotalPages: pagination.TotalPages,
			TotalCount: pagination.Total,
			Total:      pagination.Total, // For frontend compatibility
		},
	})
}

// Paginated is a convenience function for paginated list responses
func Paginated(c *gin.Context, data interface{}, page, limit int, total int64) {
	SuccessWithPagination(c, "Data retrieved successfully", data, NewPagination(page, limit, total))
}

// List sends a list response without pagination
func List(c *gin.Context, message string, data interface{}) {
	OK(c, message, data)
}

// Deleted sends a successful deletion response
func Deleted(c *gin.Context, message string) {
	OK(c, message, nil)
}

// Updated sends a successful update response
func Updated(c *gin.Context, message string, data interface{}) {
	OK(c, message, data)
}

// ValidationError sends a 422 Unprocessable Entity response
func ValidationError(c *gin.Context, message string, details interface{}) {
	Error(c, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message, details)
}

// ServiceUnavailable sends a 503 Service Unavailable response
func ServiceUnavailable(c *gin.Context, message string) {
	Error(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", message, nil)
}

// TooManyRequests sends a 429 Too Many Requests response
func TooManyRequests(c *gin.Context, message string, details interface{}) {
	Error(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", message, details)
}

// ErrorMapping maps domain errors to API error responses
// SECURITY: This prevents leaking internal error details to clients
type ErrorMapping struct {
	HTTPStatus int
	Code       string
	Message    string
}

// ErrorTranslator translates domain errors to user-friendly API responses
type ErrorTranslator struct {
	mappings map[string]ErrorMapping
	fallback ErrorMapping
}

// NewErrorTranslator creates a new error translator with default mappings
func NewErrorTranslator() *ErrorTranslator {
	return &ErrorTranslator{
		mappings: make(map[string]ErrorMapping),
		fallback: ErrorMapping{
			HTTPStatus: http.StatusInternalServerError,
			Code:       "INTERNAL_ERROR",
			Message:    "An unexpected error occurred. Please try again later.",
		},
	}
}

// Register adds an error mapping
func (t *ErrorTranslator) Register(errorText string, mapping ErrorMapping) *ErrorTranslator {
	t.mappings[errorText] = mapping
	return t
}

// Translate converts a domain error to an API error response
// It checks if the error message contains any registered error patterns
func (t *ErrorTranslator) Translate(c *gin.Context, err error, logger interface{}) {
	if err == nil {
		return
	}

	errMsg := err.Error()

	// Check for registered error patterns
	for pattern, mapping := range t.mappings {
		if containsPattern(errMsg, pattern) {
			Error(c, mapping.HTTPStatus, mapping.Code, mapping.Message, nil)
			return
		}
	}

	// Use fallback for unregistered errors
	// Log the actual error internally but don't expose to client
	Error(c, t.fallback.HTTPStatus, t.fallback.Code, t.fallback.Message, nil)
}

// containsPattern checks if the error message contains the pattern
func containsPattern(errMsg, pattern string) bool {
	return len(pattern) > 0 && len(errMsg) >= len(pattern) &&
		(errMsg == pattern ||
			len(errMsg) > len(pattern) &&
				(errMsg[:len(pattern)] == pattern ||
					errMsg[len(errMsg)-len(pattern):] == pattern ||
					contains(errMsg, pattern)))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// DefaultOrderErrorTranslator returns a pre-configured translator for order service
func DefaultOrderErrorTranslator() *ErrorTranslator {
	return NewErrorTranslator().
		Register("order not found", ErrorMapping{
			HTTPStatus: http.StatusNotFound,
			Code:       "ORDER_NOT_FOUND",
			Message:    "The requested order was not found.",
		}).
		Register("unauthorized access", ErrorMapping{
			HTTPStatus: http.StatusForbidden,
			Code:       "ACCESS_DENIED",
			Message:    "You don't have permission to access this order.",
		}).
		Register("insufficient stock", ErrorMapping{
			HTTPStatus: http.StatusConflict,
			Code:       "INSUFFICIENT_STOCK",
			Message:    "Some items in your order are no longer available.",
		}).
		Register("invalid status transition", ErrorMapping{
			HTTPStatus: http.StatusBadRequest,
			Code:       "INVALID_STATUS_TRANSITION",
			Message:    "This order cannot be updated to the requested status.",
		}).
		Register("order cannot be cancelled", ErrorMapping{
			HTTPStatus: http.StatusBadRequest,
			Code:       "CANNOT_CANCEL",
			Message:    "This order can no longer be cancelled.",
		}).
		Register("concurrent modification", ErrorMapping{
			HTTPStatus: http.StatusConflict,
			Code:       "CONCURRENT_MODIFICATION",
			Message:    "This order was modified by another process. Please refresh and try again.",
		}).
		Register("payment failed", ErrorMapping{
			HTTPStatus: http.StatusPaymentRequired,
			Code:       "PAYMENT_FAILED",
			Message:    "Payment processing failed. Please try again.",
		}).
		Register("invalid payment", ErrorMapping{
			HTTPStatus: http.StatusBadRequest,
			Code:       "INVALID_PAYMENT",
			Message:    "Payment validation failed.",
		}).
		Register("cart is empty", ErrorMapping{
			HTTPStatus: http.StatusBadRequest,
			Code:       "EMPTY_CART",
			Message:    "Your cart is empty. Add items before placing an order.",
		}).
		Register("cart not found", ErrorMapping{
			HTTPStatus: http.StatusNotFound,
			Code:       "CART_NOT_FOUND",
			Message:    "Shopping cart not found.",
		})
}

// emptyCollection turns a nil slice or nil map into an empty one so a list
// endpoint answers `"data": []` rather than `"data": null` when nothing matched.
//
// A Go handler that returns `var rows []T` with no rows hands back a nil slice,
// and encoding/json writes nil slices as `null`. Every client then has to carry a
// null guard before it can call `.map()`, and an API contract that says "a list"
// should not sometimes mean "nothing at all" (DMB-74).
//
// What it deliberately does NOT touch:
//   - an untyped nil, so `Deleted` and friends still send no `data` key at all
//     (omitempty drops it; measured in NIAGA-272, see CONVENTIONS.md §3);
//   - a nil pointer, because "the object you asked for does not exist" is genuinely
//     null and must not become `[]`;
//   - a non-nil collection, including one that is already empty.
func emptyCollection(data interface{}) interface{} {
	if data == nil {
		return nil
	}

	v := reflect.ValueOf(data)
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return reflect.MakeSlice(v.Type(), 0, 0).Interface()
		}
	case reflect.Map:
		if v.IsNil() {
			return reflect.MakeMap(v.Type()).Interface()
		}
	}

	return data
}
