package relay

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/http/httpguts"
)

type HeaderRuleAction string

const (
	HeaderRuleAdd    HeaderRuleAction = "add"
	HeaderRuleModify HeaderRuleAction = "modify"
)

type HeaderRule struct {
	Action HeaderRuleAction
	Name   string
	Value  string
}

func ParseHeaderRule(raw string, action HeaderRuleAction) (HeaderRule, error) {
	name, value, ok := strings.Cut(raw, ":")
	if !ok {
		return HeaderRule{}, fmt.Errorf("header rule %q must use Name: value format", raw)
	}

	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if !httpguts.ValidHeaderFieldName(name) {
		return HeaderRule{}, fmt.Errorf("invalid header name %q", name)
	}
	if !httpguts.ValidHeaderFieldValue(value) {
		return HeaderRule{}, fmt.Errorf("invalid header value for %q", name)
	}

	switch action {
	case HeaderRuleAdd, HeaderRuleModify:
	default:
		return HeaderRule{}, fmt.Errorf("unsupported header rule action %q", action)
	}

	return HeaderRule{
		Action: action,
		Name:   http.CanonicalHeaderKey(name),
		Value:  value,
	}, nil
}

func ParseHeaderRules(addHeaders, modifyHeaders []string) ([]HeaderRule, error) {
	rules := make([]HeaderRule, 0, len(addHeaders)+len(modifyHeaders))
	for _, raw := range addHeaders {
		rule, err := ParseHeaderRule(raw, HeaderRuleAdd)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	for _, raw := range modifyHeaders {
		rule, err := ParseHeaderRule(raw, HeaderRuleModify)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func ApplyHeaderRules(req *http.Request, rules []HeaderRule) {
	for _, rule := range rules {
		rule.Apply(req)
	}
}

func (r HeaderRule) Apply(req *http.Request) {
	if strings.EqualFold(r.Name, "Host") {
		req.Host = r.Value
		return
	}

	switch r.Action {
	case HeaderRuleAdd:
		req.Header.Add(r.Name, r.Value)
	case HeaderRuleModify:
		req.Header.Set(r.Name, r.Value)
	}
}

func (r HeaderRule) Summary() string {
	value := r.Value
	if isSensitiveHeaderName(r.Name) {
		value = "<redacted>"
	}
	return fmt.Sprintf("%s %s: %s", r.Action, r.Name, value)
}

func isSensitiveHeaderName(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key", "X-Auth-Token":
		return true
	default:
		return false
	}
}
