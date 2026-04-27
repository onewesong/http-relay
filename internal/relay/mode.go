package relay

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type TargetModeKind uint8

const (
	TargetModeAbsoluteURL TargetModeKind = iota
	TargetModeReverse
)

type TargetMode struct {
	kind        TargetModeKind
	reverseBase *url.URL
}

func DefaultTargetMode() TargetMode {
	return TargetMode{kind: TargetModeAbsoluteURL}
}

func ParseMode(raw string) (TargetMode, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "regular") || strings.EqualFold(raw, "relay") {
		return DefaultTargetMode(), nil
	}

	if strings.HasPrefix(strings.ToLower(raw), "reverse:") {
		baseRaw := strings.TrimSpace(raw[len("reverse:"):])
		if baseRaw == "" {
			return TargetMode{}, errors.New("reverse mode requires an upstream URL")
		}

		base, err := url.Parse(baseRaw)
		if err != nil {
			return TargetMode{}, fmt.Errorf("invalid reverse upstream URL: %w", err)
		}
		if !isHTTPURL(base) {
			return TargetMode{}, errors.New("reverse upstream URL must start with http:// or https://")
		}
		base.Fragment = ""

		return TargetMode{kind: TargetModeReverse, reverseBase: base}, nil
	}

	return TargetMode{}, fmt.Errorf("unsupported mode %q", raw)
}

func (m TargetMode) String() string {
	switch m.kind {
	case TargetModeReverse:
		if m.reverseBase == nil {
			return "reverse <invalid>"
		}
		return "reverse " + m.reverseBase.String()
	default:
		return "regular"
	}
}

func (m TargetMode) TargetURL(r *http.Request) (*url.URL, error) {
	switch m.kind {
	case TargetModeReverse:
		if m.reverseBase == nil {
			return nil, errors.New("reverse mode is missing upstream URL")
		}
		return buildReverseTargetURL(m.reverseBase, r), nil
	default:
		return parseTargetURL(r)
	}
}

func buildReverseTargetURL(base *url.URL, r *http.Request) *url.URL {
	target := *base
	target.Path, target.RawPath = joinURLPath(base, r.URL)
	target.RawQuery = joinRawQuery(base.RawQuery, r.URL.RawQuery)
	return &target
}

func isHTTPURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}

func joinRawQuery(baseQuery, requestQuery string) string {
	switch {
	case baseQuery == "":
		return requestQuery
	case requestQuery == "":
		return baseQuery
	default:
		return baseQuery + "&" + requestQuery
	}
}

func joinURLPath(base, request *url.URL) (path, rawpath string) {
	if base.RawPath == "" && request.RawPath == "" {
		return singleJoiningSlash(base.Path, request.Path), ""
	}

	basePath := base.EscapedPath()
	requestPath := request.EscapedPath()
	baseSlash := strings.HasSuffix(basePath, "/")
	requestSlash := strings.HasPrefix(requestPath, "/")

	switch {
	case baseSlash && requestSlash:
		return base.Path + request.Path[1:], basePath + requestPath[1:]
	case !baseSlash && !requestSlash:
		return base.Path + "/" + request.Path, basePath + "/" + requestPath
	default:
		return base.Path + request.Path, basePath + requestPath
	}
}

func singleJoiningSlash(basePath, requestPath string) string {
	baseSlash := strings.HasSuffix(basePath, "/")
	requestSlash := strings.HasPrefix(requestPath, "/")

	switch {
	case baseSlash && requestSlash:
		return basePath + requestPath[1:]
	case !baseSlash && !requestSlash:
		return basePath + "/" + requestPath
	default:
		return basePath + requestPath
	}
}
