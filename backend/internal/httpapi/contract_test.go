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

// implementedMilestones is the one place that says which milestones have landed. It gains
// a line per milestone, and that line is the diff that says "this shipped".
var implementedMilestones = map[string]bool{
	"M1": true,
	"M2": true,
	"M3": true,
	"M4": true,
	"M5": true,
}

// fillPath substitutes the path parameters. podId is a pod the caller really belongs to,
// so a routed-but-empty answer is distinguishable from an unrouted one; the M3 ids are
// still synthetic because nothing reads them yet.
func fillPath(template string, ids pathIDs) string {
	replacer := strings.NewReplacer(
		"{podId}", ids.pod,
		"{weekId}", ids.week,
		"{proofId}", ids.proof,
	)
	return replacer.Replace(template)
}

// pathIDs are real resources the caller can reach, so that a routed operation answering
// "not found" is distinguishable from one that was never registered.
type pathIDs struct{ pod, week, proof string }

// Every operation in the contract must be routed. A specified route that answers 404 is
// indistinguishable from a client typo, which is exactly what ADR-007 is about.
func TestEveryContractOperationIsRouted(t *testing.T) {
	h := newHarness(t)
	session := h.signIn("sam@example.com")
	ids := h.seedWeekWithProof(session.User.ID)

	for _, op := range loadContract(t) {
		t.Run(op.id, func(t *testing.T) {
			req := httptest.NewRequest(op.method, fillPath(op.path, ids), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+session.AccessToken)
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound {
				t.Fatalf("%s %s is in the contract but not routed", op.method, op.path)
			}
			if implementedMilestones[op.milestone] {
				if rec.Code == http.StatusNotImplemented {
					t.Fatalf("%s is marked %s, which has landed, but answers 501", op.id, op.milestone)
				}
				return
			}
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s is marked %s but answers %d — either it shipped early or implementedMilestones is stale", op.id, op.milestone, rec.Code)
			}
			var body api.ApiError
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Code != api.ErrorCodeNotImplementedYet {
				t.Fatalf("%s: code %q, want not_implemented_yet", op.id, body.Code)
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
			req := httptest.NewRequest(op.method, fillPath(op.path, pathIDs{
				pod:   "11111111-1111-4111-8111-111111111111",
				week:  "22222222-2222-4222-8222-222222222222",
				proof: "33333333-3333-4333-8333-333333333333",
			}), strings.NewReader("{}"))
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
