package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omarharoun/pod/backend/internal/api"
)

// docs/API.md is generated from backend/openapi.yaml, so parsing it is the same as
// reading the contract — with the advantage that it needs no YAML dependency here.
var operationRow = regexp.MustCompile(`^\| (M\d) \| ` + "`" + `([A-Z]+)` + "`" + ` \| ` + "`" + `([^` + "`" + `]+)` + "`" + ` \| ` + "`" + `([a-zA-Z]+)` + "`" + ` \| (\w+) \|$`)

type contractOperation struct {
	milestone string
	method    string
	path      string
	id        string
	public    bool
}

func loadContract(t *testing.T) []contractOperation {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "API.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — run `make generate`", path, err)
	}
	var ops []contractOperation
	for _, line := range strings.Split(string(raw), "\n") {
		m := operationRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		ops = append(ops, contractOperation{milestone: m[1], method: m[2], path: m[3], id: m[4], public: m[5] == "public"})
	}
	if len(ops) == 0 {
		t.Fatalf("parsed no operations out of %s — the generated format changed", path)
	}
	return ops
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorCode {
	t.Helper()
	var body api.ApiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return ""
	}
	return body.Code
}

// fillPath substitutes the path parameters with syntactically valid values. The routes
// under test do not read them yet; they only have to route.
func fillPath(template string) string {
	replacer := strings.NewReplacer(
		"{podId}", "11111111-1111-4111-8111-111111111111",
		"{weekId}", "22222222-2222-4222-8222-222222222222",
		"{proofId}", "33333333-3333-4333-8333-333333333333",
	)
	return replacer.Replace(template)
}

// Every operation in the contract must be routed. A specified route that answers 404 is
// indistinguishable from a client typo, which is exactly what ADR-007 is about.
func TestEveryContractOperationIsRouted(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")

	for _, op := range loadContract(t) {
		t.Run(op.id, func(t *testing.T) {
			req := httptest.NewRequest(op.method, fillPath(op.path), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+session.AccessToken)
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s is in the contract but not routed", op.method, op.path)
			}
			switch op.milestone {
			case "M1":
				if rec.Code == http.StatusNotImplemented {
					t.Fatalf("%s is marked M1 but answers 501", op.id)
				}
			default:
				if rec.Code != http.StatusNotImplemented {
					t.Fatalf("%s is marked %s but answers %d — either it shipped early or its x-milestone is stale", op.id, op.milestone, rec.Code)
				}
				var body api.ApiError
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if body.Code != api.ErrorCodeNotImplementedYet {
					t.Fatalf("%s: code %q, want not_implemented_yet", op.id, body.Code)
				}
			}
		})
	}
}

// Authentication is part of the contract too: an operation the spec marks as requiring a
// bearer token must actually require one.
func TestContractAuthenticationMatchesTheRouter(t *testing.T) {
	h := newHarness(t)

	for _, op := range loadContract(t) {
		t.Run(op.id, func(t *testing.T) {
			req := httptest.NewRequest(op.method, fillPath(op.path), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, req)

			// A 401 alone is not the signal: `verifyAuth` legitimately answers 401 for a
			// wrong code. What distinguishes the middleware is the error code.
			rejectedByMiddleware := rec.Code == http.StatusUnauthorized && errorCode(t, rec) == api.ErrorCodeSessionExpired

			if op.public {
				if rejectedByMiddleware {
					t.Fatalf("%s is public in the contract but the auth middleware rejected it", op.id)
				}
				return
			}
			if !rejectedByMiddleware {
				t.Fatalf("%s requires bearer auth in the contract but answered %d without a token: %s", op.id, rec.Code, rec.Body.String())
			}
		})
	}
}
