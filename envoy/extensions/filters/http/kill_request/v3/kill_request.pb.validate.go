// Code generated by protoc-gen-validate. DO NOT EDIT.
// source: envoy/extensions/filters/http/kill_request/v3/kill_request.proto

package envoy_extensions_filters_http_kill_request_v3

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang/protobuf/ptypes"
)

// ensure the imports are used
var (
	_ = bytes.MinRead
	_ = errors.New("")
	_ = fmt.Print
	_ = utf8.UTFMax
	_ = (*regexp.Regexp)(nil)
	_ = (*strings.Reader)(nil)
	_ = net.IPv4len
	_ = time.Duration(0)
	_ = (*url.URL)(nil)
	_ = (*mail.Address)(nil)
	_ = ptypes.DynamicAny{}
)

// define the regex for a UUID once up-front
var _kill_request_uuidPattern = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

// Validate checks the field values on KillRequest with the rules defined in
// the proto definition for this message. If any rules are violated, an error
// is returned.
func (m *KillRequest) Validate() error {
	if m == nil {
		return nil
	}

	if v, ok := interface{}(m.GetProbability()).(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return KillRequestValidationError{
				field:  "Probability",
				reason: "embedded message failed validation",
				cause:  err,
			}
		}
	}

	// no validation rules for KillRequestHeader

	return nil
}

// KillRequestValidationError is the validation error returned by
// KillRequest.Validate if the designated constraints aren't met.
type KillRequestValidationError struct {
	field  string
	reason string
	cause  error
	key    bool
}

// Field function returns field value.
func (e KillRequestValidationError) Field() string { return e.field }

// Reason function returns reason value.
func (e KillRequestValidationError) Reason() string { return e.reason }

// Cause function returns cause value.
func (e KillRequestValidationError) Cause() error { return e.cause }

// Key function returns key value.
func (e KillRequestValidationError) Key() bool { return e.key }

// ErrorName returns error name.
func (e KillRequestValidationError) ErrorName() string { return "KillRequestValidationError" }

// Error satisfies the builtin error interface
func (e KillRequestValidationError) Error() string {
	cause := ""
	if e.cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.cause)
	}

	key := ""
	if e.key {
		key = "key for "
	}

	return fmt.Sprintf(
		"invalid %sKillRequest.%s: %s%s",
		key,
		e.field,
		e.reason,
		cause)
}

var _ error = KillRequestValidationError{}

var _ interface {
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
} = KillRequestValidationError{}
