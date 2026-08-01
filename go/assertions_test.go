// SPDX-License-Identifier: EUPL-1.2

package ratelimit

import (
	core "dappco.re/go"
	"math"
	"reflect"
	"time"
)

func testFailureMessage(defaultMsg string, msgAndArgs ...any) string {
	if len(msgAndArgs) == 0 {
		return defaultMsg
	}

	msg := core.Sprint(msgAndArgs...)
	if format, ok := msgAndArgs[0].(string); ok && len(msgAndArgs) > 1 {
		msg = core.Sprintf(format, msgAndArgs[1:]...)
	}
	if defaultMsg == "" {
		return msg
	}
	return msg + ": " + defaultMsg
}

func testUnexpectedErrorMessage(err error, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("unexpected error: %v", err), msgAndArgs...)
}

func testExpectedErrorMessage(msgAndArgs ...any) string {
	return testFailureMessage("expected error, got nil", msgAndArgs...)
}

func testWantGotMessage(want, got any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("want %v, got %v", want, got), msgAndArgs...)
}

func testExpectedTrueMessage(msgAndArgs ...any) string {
	return testFailureMessage("expected true", msgAndArgs...)
}

func testExpectedFalseMessage(msgAndArgs ...any) string {
	return testFailureMessage("expected false", msgAndArgs...)
}

func testExpectedNilMessage(v any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected nil, got %v", v), msgAndArgs...)
}

func testExpectedNonNilMessage(msgAndArgs ...any) string {
	return testFailureMessage("expected non-nil", msgAndArgs...)
}

func testContainsMessage(container, elem any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected %v to contain %v", container, elem), msgAndArgs...)
}

func testNotContainsMessage(container, elem any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected %v not to contain %v", container, elem), msgAndArgs...)
}

func testEmptyMessage(v any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected empty, got %v", v), msgAndArgs...)
}

func testLenMessage(v any, want int, msgAndArgs ...any) string {
	if got, ok := testLenOf(v); ok {
		return testFailureMessage(core.Sprintf("expected length %d, got %d", want, got), msgAndArgs...)
	}
	return testFailureMessage(core.Sprintf("expected length %d, got non-len value %v", want, v), msgAndArgs...)
}

func testErrorIsMessage(err, target error, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected error %v to match %v", err, target), msgAndArgs...)
}

func testInDeltaMessage(want, got, delta any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected %v and %v to be within %v", want, got, delta), msgAndArgs...)
}

func testZeroMessage(v any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected zero value, got %v", v), msgAndArgs...)
}

func testUnexpectedPanicMessage(recovered any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("unexpected panic: %v", recovered), msgAndArgs...)
}

func testGreaterOrEqualMessage(a, b any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected %v >= %v", a, b), msgAndArgs...)
}

func testGreaterMessage(a, b any, msgAndArgs ...any) string {
	return testFailureMessage(core.Sprintf("expected %v > %v", a, b), msgAndArgs...)
}

func testEventuallyMessage(msgAndArgs ...any) string {
	return testFailureMessage("condition was not satisfied before timeout", msgAndArgs...)
}

func testEqual(want, got any) bool {
	return reflect.DeepEqual(want, got)
}

func testIsNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func testContains(container, elem any) bool {
	if container == nil {
		return false
	}

	if s, ok := container.(string); ok {
		needle, ok := elem.(string)
		if !ok {
			needle = core.Sprint(elem)
		}
		return core.Contains(s, needle)
	}

	cv := reflect.ValueOf(container)
	switch cv.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(elem)
		if !key.IsValid() {
			return false
		}
		if key.Type().AssignableTo(cv.Type().Key()) {
			return cv.MapIndex(key).IsValid()
		}
		if key.Type().ConvertibleTo(cv.Type().Key()) {
			return cv.MapIndex(key.Convert(cv.Type().Key())).IsValid()
		}
	case reflect.Slice, reflect.Array:
		for i := range cv.Len() {
			if reflect.DeepEqual(cv.Index(i).Interface(), elem) {
				return true
			}
		}
	}
	return false
}

func testIsEmpty(v any) bool {
	if testIsNil(v) {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len() == 0
	default:
		return rv.IsZero()
	}
}

func testLenOf(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len(), true
	default:
		return 0, false
	}
}

func testHasLen(v any, want int) bool {
	got, ok := testLenOf(v)
	return ok && got == want
}

func testErrorIs(err, target error) bool {
	return core.Is(err, target)
}

func testInDelta(want, got, delta any) bool {
	wantFloat, ok := testFloat(want)
	if !ok {
		return false
	}
	gotFloat, ok := testFloat(got)
	if !ok {
		return false
	}
	deltaFloat, ok := testFloat(delta)
	if !ok {
		return false
	}
	return math.Abs(wantFloat-gotFloat) <= math.Abs(deltaFloat)
}

func testIsZero(v any) bool {
	if testIsNil(v) {
		return true
	}
	return reflect.ValueOf(v).IsZero()
}

func testRecoverPanic(fn func()) (recovered any) {
	defer func() {
		recovered = recover()
	}()
	fn()
	return nil
}

func testGreaterOrEqual(a, b any) bool {
	af, ok := testFloat(a)
	if !ok {
		return false
	}
	bf, ok := testFloat(b)
	return ok && af >= bf
}

func testGreater(a, b any) bool {
	af, ok := testFloat(a)
	if !ok {
		return false
	}
	bf, ok := testFloat(b)
	return ok && af > bf
}

func testEventually(fn func() bool, waitFor, tick time.Duration) bool {
	if fn() {
		return true
	}

	deadline := time.NewTimer(waitFor)
	defer deadline.Stop()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-deadline.C:
			return fn()
		case <-ticker.C:
			if fn() {
				return true
			}
		}
	}
}

func testFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}
