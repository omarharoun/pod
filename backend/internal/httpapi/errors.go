package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/omarharoun/pod/backend/internal/api"
	"github.com/omarharoun/pod/backend/internal/copy"
)

// failure is one API error, ready to be written. Every failure the service can produce
// is constructed through one of the helpers below, so there is no path to a response
// carrying a copy key that does not exist (ADR-003).
type failure struct {
	status     int
	code       api.ErrorCode
	copyKey    copy.Key
	params     map[string]string
	retryAfter int
}

func badRequest() failure {
	return failure{status: http.StatusBadRequest, code: api.ErrorCodeBadRequest, copyKey: copy.KeyErrorServerBody}
}

func invalidEmail() failure {
	return failure{status: http.StatusBadRequest, code: api.ErrorCodeBadRequest, copyKey: copy.KeyAuthStartErrorEmailInvalid}
}

// unauthorized is the access-token failure: the bearer token is missing, malformed or
// past its 15 minutes. The code tells the client to try a refresh before giving up.
func unauthorized() failure {
	return failure{status: http.StatusUnauthorized, code: api.ErrorCodeSessionExpired, copyKey: copy.KeyErrorSessionExpiredBody}
}

// refreshRejected is the refresh-token failure, and is deliberately a different code:
// there is nothing left to retry, so the client signs the person in again.
func refreshRejected() failure {
	return failure{status: http.StatusUnauthorized, code: api.ErrorCodeUnauthorized, copyKey: copy.KeyErrorSessionExpiredBody}
}

func codeIncorrect() failure {
	return failure{status: http.StatusUnauthorized, code: api.ErrorCodeCodeIncorrect, copyKey: copy.KeyAuthVerifyErrorCodeIncorrect}
}

func codeExpired() failure {
	return failure{status: http.StatusUnauthorized, code: api.ErrorCodeCodeExpired, copyKey: copy.KeyAuthVerifyErrorCodeExpired}
}

func tooManyAttempts() failure {
	return failure{status: http.StatusUnauthorized, code: api.ErrorCodeTooManyAttempts, copyKey: copy.KeyAuthVerifyErrorTooManyAttempts}
}

func rateLimited(retryAfterSeconds int) failure {
	return failure{
		status:     http.StatusTooManyRequests,
		code:       api.ErrorCodeRateLimited,
		copyKey:    copy.KeyErrorRateLimitedBody,
		params:     map[string]string{"seconds": strconv.Itoa(retryAfterSeconds)},
		retryAfter: retryAfterSeconds,
	}
}

func goalTooShort() failure {
	return failure{status: http.StatusBadRequest, code: api.ErrorCodeBadRequest, copyKey: copy.KeyErrorGoalTooShortBody}
}

func alreadyInPod() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorAlreadyInPodBody}
}

// cycleChoiceClosed covers both "the pod is not asking" and "the deadline has passed".
// The two are the same thing from the screen's point of view — the choice moved on — and
// distinguishing them would only be useful for telling someone they were too late.
func cycleChoiceClosed() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorCycleChoiceClosedBody}
}

func weekClosed() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorWeekClosedBody}
}

func alreadyPosted() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorAlreadyPostedBody}
}

func actionLocked() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorActionLockedBody}
}

func proofTooLong() failure {
	return failure{status: http.StatusBadRequest, code: api.ErrorCodeBadRequest, copyKey: copy.KeyErrorProofTooLongBody}
}

func proofTooLarge() failure {
	return failure{status: http.StatusBadRequest, code: api.ErrorCodeBadRequest, copyKey: copy.KeyErrorProofTooLargeBody}
}

func uploadIncomplete() failure {
	return failure{status: http.StatusConflict, code: api.ErrorCodeConflict, copyKey: copy.KeyErrorUploadIncompleteBody}
}

func notFound() failure {
	return failure{status: http.StatusNotFound, code: api.ErrorCodeNotFound, copyKey: copy.KeyErrorServerBody}
}

// notImplementedYet is the answer for a route whose milestone has not landed (ADR-007).
// It is distinct from 404 so that a client calling it early gets told why.
func notImplementedYet() failure {
	return failure{status: http.StatusNotImplemented, code: api.ErrorCodeNotImplementedYet, copyKey: copy.KeyErrorServerBody}
}

func serverError() failure {
	return failure{status: http.StatusInternalServerError, code: api.ErrorCodeServerError, copyKey: copy.KeyErrorServerBody}
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, f failure, cause error) {
	requestID := RequestIDFrom(r.Context())
	if f.status >= http.StatusInternalServerError {
		s.log.ErrorContext(r.Context(), "request failed",
			slog.String("request_id", requestID),
			slog.String("code", string(f.code)),
			slog.Any("error", cause),
		)
	} else if cause != nil {
		s.log.InfoContext(r.Context(), "request rejected",
			slog.String("request_id", requestID),
			slog.String("code", string(f.code)),
			slog.Any("error", cause),
		)
	}

	body := api.ApiError{
		Code:      f.code,
		CopyKey:   string(f.copyKey),
		Params:    f.params,
		RequestID: requestID,
	}
	if f.retryAfter > 0 {
		body.RetryAfterSeconds = &f.retryAfter
		w.Header().Set("Retry-After", strconv.Itoa(f.retryAfter))
	}
	s.writeJSON(w, r, f.status, body)
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already out; all that is left is to say so.
		s.log.ErrorContext(r.Context(), "encoding the response body failed",
			slog.String("request_id", RequestIDFrom(r.Context())),
			slog.Any("error", err),
		)
	}
}

// decodeJSON reads a request body into dst, rejecting unknown fields and trailing
// content. Strictness here is what stops a client typo from silently doing nothing.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		return errTrailingContent
	}
	return nil
}

var errTrailingContent = &jsonError{"unexpected content after the JSON body"}

type jsonError struct{ msg string }

func (e *jsonError) Error() string { return e.msg }
