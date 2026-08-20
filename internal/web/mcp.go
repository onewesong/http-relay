package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onewesong/http-relay/internal/authjwt"
	appconfig "github.com/onewesong/http-relay/internal/config"
)

type mcpAuthKey struct{}
type mcpAuthState struct {
	claims authjwt.Claims
	valid  bool
}

const (
	mcpDefaultLimit = 50
	mcpMaxLimit     = 200
)

type mcpListInput struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"namespace to query"`
	Limit         int    `json:"limit,omitempty" jsonschema:"maximum number of records"`
	IncludeBodies *bool  `json:"include_bodies,omitempty"`
}
type mcpGetInput struct {
	Seq       uint64 `json:"seq"`
	Namespace string `json:"namespace,omitempty"`
}
type mcpSearchInput struct {
	Namespace      string `json:"namespace,omitempty"`
	Method         string `json:"method,omitempty"`
	TargetContains string `json:"target_contains,omitempty"`
	Status         *int   `json:"status,omitempty"`
	StatusMin      *int   `json:"status_min,omitempty"`
	StatusMax      *int   `json:"status_max,omitempty"`
	HasError       *bool  `json:"has_error,omitempty"`
	Done           *bool  `json:"done,omitempty"`
	From           string `json:"from,omitempty"`
	To             string `json:"to,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}
type mcpAnalyzeInput = mcpSearchInput
type mcpTransactionsOutput struct {
	Namespace    string         `json:"namespace"`
	Count        int            `json:"count"`
	Transactions []*Transaction `json:"transactions"`
}
type mcpAnalyzeOutput struct {
	Count             int            `json:"count"`
	Success           int            `json:"success"`
	Failed            int            `json:"failed"`
	ByStatus          map[string]int `json:"by_status"`
	ByMethod          map[string]int `json:"by_method"`
	AverageDurationMs float64        `json:"average_duration_ms"`
	MinDurationMs     int64          `json:"min_duration_ms"`
	MaxDurationMs     int64          `json:"max_duration_ms"`
	TotalBytes        int64          `json:"total_bytes"`
	Errors            []mcpErrorItem `json:"errors,omitempty"`
	Transactions      []*Transaction `json:"transactions,omitempty"`
}
type mcpErrorItem struct {
	Seq   uint64 `json:"seq"`
	Error string `json:"error"`
}

func mcpContext(ctx context.Context) mcpAuthState {
	v, _ := ctx.Value(mcpAuthKey{}).(mcpAuthState)
	return v
}

func (a *authenticator) mcpMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := mcpAuthState{}
		rawAuthorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if rawAuthorization != "" {
			token, present := bearerToken(r)
			if !present {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if !a.jwtEnabled() {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			claims, err := a.verifyJWT(token)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			state = mcpAuthState{claims: claims, valid: true}
		}
		ctx := context.WithValue(r.Context(), mcpAuthKey{}, state)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *authenticator) mcpHandler(s *store, version string) http.Handler {
	getServer := func(r *http.Request) *mcp.Server {
		server := mcp.NewServer(&mcp.Implementation{Name: "http-relay", Version: version}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "list_transactions", Description: "List recent captured HTTP transactions."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpListInput) (*mcp.CallToolResult, mcpTransactionsOutput, error) {
			ns, err := a.mcpNamespace(ctx, in.Namespace)
			if err != nil {
				return nil, mcpTransactionsOutput{}, err
			}
			limit := normalizeMCPLimit(in.Limit)
			items := s.query(ns, TransactionFilter{}, limit)
			if in.IncludeBodies != nil && !*in.IncludeBodies {
				items = stripBodies(items)
			}
			return nil, mcpTransactionsOutput{Namespace: ns, Count: len(items), Transactions: items}, nil
		})
		mcp.AddTool(server, &mcp.Tool{Name: "get_transaction", Description: "Get one captured HTTP transaction by sequence number."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpGetInput) (*mcp.CallToolResult, *Transaction, error) {
			ns, err := a.mcpNamespace(ctx, in.Namespace)
			if err != nil {
				return nil, nil, err
			}
			item, ok := s.transaction(ns, in.Seq)
			if !ok {
				return nil, nil, fmt.Errorf("transaction not found")
			}
			return nil, item, nil
		})
		mcp.AddTool(server, &mcp.Tool{Name: "search_transactions", Description: "Search captured transactions with filters."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, mcpTransactionsOutput, error) {
			return mcpSearch(ctx, a, s, in)
		})
		mcp.AddTool(server, &mcp.Tool{Name: "analyze_transactions", Description: "Analyze captured transactions and return aggregate statistics."}, func(ctx context.Context, _ *mcp.CallToolRequest, in mcpAnalyzeInput) (*mcp.CallToolResult, mcpAnalyzeOutput, error) {
			ns, err := a.mcpNamespace(ctx, in.Namespace)
			if err != nil {
				return nil, mcpAnalyzeOutput{}, err
			}
			items, err := mcpQuery(s, ns, in)
			if err != nil {
				return nil, mcpAnalyzeOutput{}, err
			}
			return nil, analyze(items), nil
		})
		return server
	}
	return mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{JSONResponse: true, Stateless: true, MaxRequestBodyBytes: 4 << 20})
}

func (a *authenticator) mcpNamespace(ctx context.Context, requested string) (string, error) {
	state := mcpContext(ctx)
	requested = strings.TrimSpace(requested)
	if requested != "" && !validNamespace(requested) {
		return "", fmt.Errorf("invalid namespace")
	}
	if state.valid {
		if state.claims.Namespace != "" && requested != "" && requested != state.claims.Namespace {
			return "", fmt.Errorf("namespace access denied")
		}
		if state.claims.Namespace != "" {
			return state.claims.Namespace, nil
		}
		return requested, nil
	}
	if requested != "" && a.protected(requested) {
		return "", fmt.Errorf("authentication required")
	}
	if requested == "" && a.protected("") {
		return "", fmt.Errorf("authentication required")
	}
	return requested, nil
}

func validNamespace(ns string) bool { return ns == "" || appconfig.ValidNamespace(ns) }

func normalizeMCPLimit(v int) int {
	if v <= 0 {
		return mcpDefaultLimit
	}
	if v > mcpMaxLimit {
		return mcpMaxLimit
	}
	return v
}
func stripBodies(items []*Transaction) []*Transaction {
	for _, t := range items {
		t.ReqBody = nil
		t.RespBody = nil
	}
	return items
}

func mcpSearch(ctx context.Context, a *authenticator, s *store, in mcpSearchInput) (*mcp.CallToolResult, mcpTransactionsOutput, error) {
	ns, err := a.mcpNamespace(ctx, in.Namespace)
	if err != nil {
		return nil, mcpTransactionsOutput{}, err
	}
	items, err := mcpQuery(s, ns, in)
	if err != nil {
		return nil, mcpTransactionsOutput{}, err
	}
	return nil, mcpTransactionsOutput{Namespace: ns, Count: len(items), Transactions: items}, nil
}

func mcpQuery(s *store, ns string, in mcpSearchInput) ([]*Transaction, error) {
	filter := TransactionFilter{Method: in.Method, TargetContains: in.TargetContains, Status: in.Status, StatusMin: in.StatusMin, StatusMax: in.StatusMax, HasError: in.HasError, Done: in.Done}
	var err error
	if in.From != "" {
		filter.From, err = parseMCPTime(in.From)
		if err != nil {
			return nil, err
		}
	}
	if in.To != "" {
		filter.To, err = parseMCPTime(in.To)
		if err != nil {
			return nil, err
		}
	}
	limit := normalizeMCPLimit(in.Limit)
	return s.query(ns, filter, limit), nil
}
func parseMCPTime(v string) (*time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("invalid time %q", v)
	}
	return &t, nil
}

func analyze(items []*Transaction) mcpAnalyzeOutput {
	out := mcpAnalyzeOutput{Count: len(items), ByStatus: map[string]int{}, ByMethod: map[string]int{}}
	if len(items) == 0 {
		return out
	}
	var total int64
	out.MinDurationMs = items[0].DurationMs
	for _, t := range items {
		if t.Done && t.Status >= 200 && t.Status < 400 {
			out.Success++
		} else if t.Err != "" || (t.Done && t.Status >= 400) {
			out.Failed++
		}
		out.ByStatus[strconv.Itoa(t.Status)]++
		out.ByMethod[t.Method]++
		total += t.DurationMs
		out.TotalBytes += t.Bytes
		if t.DurationMs < out.MinDurationMs {
			out.MinDurationMs = t.DurationMs
		}
		if t.DurationMs > out.MaxDurationMs {
			out.MaxDurationMs = t.DurationMs
		}
		if t.Err != "" {
			out.Errors = append(out.Errors, mcpErrorItem{Seq: t.Seq, Error: t.Err})
		}
	}
	out.AverageDurationMs = float64(total) / float64(len(items))
	out.Transactions = items
	sort.Slice(out.Errors, func(i, j int) bool { return out.Errors[i].Seq < out.Errors[j].Seq })
	return out
}
