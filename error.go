// This source file is part of the EdgeDB open source project.
//
// Copyright 2020-present EdgeDB Inc. and the EdgeDB authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package edgedb

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/edgedb/edgedb-go/internal/buff"
)

var errZeroResults error = &noDataError{msg: "zero results"}

// ErrorTag is the argument type to Error.HasTag().
type ErrorTag string

// ErrorCategory values represent EdgeDB's error types.
type ErrorCategory string

// Error is the error type returned from edgedb.
type Error interface {
	Error() string
	Unwrap() error

	// HasTag returns true if the error is marked with the supplied tag.
	HasTag(ErrorTag) bool

	// Category returns true if the error is in the provided category.
	Category(ErrorCategory) bool
}

// firstError returns the first non nil error or nil.
func firstError(a, b error) error {
	if a != nil {
		return a
	}

	return b
}

const (
	hint          = 0x0001
	positionStart = 0xfff1
	lineStart     = 0xfff3
)

func atoiOrPanic(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}

	return i
}

type position struct {
	lineNo int
	byteNo int
}

func positionFromHeaders(headers map[uint16]string) (position, bool) {
	lineNo, ok := headers[lineStart]
	if !ok {
		return position{}, false
	}

	byteNo, ok := headers[positionStart]
	if !ok {
		return position{}, false
	}

	return position{
		lineNo: atoiOrPanic(lineNo) - 1,
		byteNo: atoiOrPanic(byteNo),
	}, true
}

// decodeError decodes an error response
// https://www.edgedb.com/docs/internals/protocol/messages#errorresponse
func decodeError(r *buff.Reader, query string) error {
	r.Discard(1) // severity
	code := r.PopUint32()
	msg := r.PopString()

	n := int(r.PopUint16())
	headers := make(map[uint16]string, n)
	for i := 0; i < n; i++ {
		headers[r.PopUint16()] = r.PopString()
	}

	pos, ok := positionFromHeaders(headers)
	if !ok {
		return errorFromCode(code, msg)
	}

	hintmsg, ok := headers[hint]
	if !ok {
		hintmsg = "error"
	}

	lines := strings.Split(query, "\n")

	// replace tabs with a single space
	// because we don't know how they will be printed.
	line := strings.ReplaceAll(lines[pos.lineNo], "\t", " ")

	for i := 0; i < pos.lineNo; i++ {
		pos.byteNo -= 1 + len(lines[i])
	}

	runeCount := utf8.RuneCountInString(line[:pos.byteNo])
	padding := strings.Repeat(" ", runeCount)

	msg += fmt.Sprintf(
		"\nquery:%v:%v\n\n%v\n%v^ %v",
		1+pos.lineNo,
		1+runeCount,
		line,
		padding,
		hintmsg,
	)

	return errorFromCode(code, msg)
}

type wrappedManyError struct {
	msg  string
	errs []error
}

func (e *wrappedManyError) Error() string {
	return e.msg
}

func (e *wrappedManyError) Is(target error) bool {
	for _, err := range e.errs {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}

func (e *wrappedManyError) As(target interface{}) bool {
	for _, err := range e.errs {
		if errors.As(err, target) {
			return true
		}
	}

	return false
}

func wrapAll(errs ...error) error {
	err := &wrappedManyError{}
	for _, e := range errs {
		if e != nil {
			err.errs = append(err.errs, e)
		}
	}

	if len(err.errs) == 0 {
		return nil
	}

	if len(err.errs) == 1 {
		return err.errs[0]
	}

	err.msg = err.errs[0].Error()
	for _, e := range err.errs[1:] {
		err.msg += "; " + e.Error()
	}

	return err
}
