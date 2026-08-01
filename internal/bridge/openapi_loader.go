package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	openAPIImportTimeout      = 10 * time.Second
	maxOpenAPIBodyBytes       = 5 << 20
	maxOpenAPITotalBytes      = 20 << 20
	maxOpenAPIDocuments       = 16
	maxOpenAPIRedirects       = 5
	maxOpenAPIReferenceDepth  = 64
	maxOpenAPIStructureDepth  = 256
	maxOpenAPIReferences      = 10_000
	maxOpenAPIResolvedNodes   = 200_000
	maxOpenAPIAnchorScanNodes = 200_000
)

var (
	openAPIHTTPClient      = http.DefaultClient
	openAPILookupIP        = net.DefaultResolver.LookupNetIP
	blockedOpenAPINetworks = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
)

type loadedOpenAPIDocument struct {
	url  string
	root map[string]any
}

type openAPILoader struct {
	ctx                context.Context
	request            OpenAPIImportRequest
	client             *http.Client
	closeIdle          func()
	documents          map[string]*loadedOpenAPIDocument
	resolvedReferences map[string]any
	documentCount      int
	redirectCount      int
	totalBytes         int
	referenceCount     int
	resolvedNodes      int
}

func loadOpenAPIDocument(
	ctx context.Context,
	request OpenAPIImportRequest,
) (string, map[string]any, error) {
	rootURL, err := parseOpenAPIURL(request.URL)
	if err != nil {
		return "", nil, err
	}
	loader := newOpenAPILoader(ctx, request)
	defer loader.closeIdle()

	root, err := loader.loadDocument(rootURL)
	if err != nil {
		return "", nil, err
	}
	resolved, err := loader.resolveValue(root.root, root.url, map[string]bool{}, 0, 0)
	if err != nil {
		return "", nil, err
	}
	document, ok := resolved.(map[string]any)
	if !ok {
		return "", nil, errors.New("OpenAPI document root must be an object")
	}
	return root.url, document, nil
}

func newOpenAPILoader(ctx context.Context, request OpenAPIImportRequest) *openAPILoader {
	client, closeIdle := newOpenAPIImportClient(request.AllowPrivateNetwork)
	return &openAPILoader{
		ctx:                ctx,
		request:            request,
		client:             client,
		closeIdle:          closeIdle,
		documents:          make(map[string]*loadedOpenAPIDocument),
		resolvedReferences: make(map[string]any),
	}
}

func newOpenAPIImportClient(allowPrivateNetwork bool) (*http.Client, func()) {
	base := openAPIHTTPClient
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	closeIdle := func() {}
	if !allowPrivateNetwork && base.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = safeOpenAPIDialContext
		client.Transport = transport
		closeIdle = transport.CloseIdleConnections
	}
	return &client, closeIdle
}

func safeOpenAPIDialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("validate OpenAPI connection address: %w", err)
	}
	addresses, err := resolveOpenAPIHost(ctx, host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	dialer := net.Dialer{}
	for _, address := range addresses {
		if isUnsafeOpenAPIAddress(address) {
			return nil, fmt.Errorf("OpenAPI host %q resolves to a private or reserved address", host)
		}
		connection, dialErr := dialer.DialContext(
			ctx,
			network,
			net.JoinHostPort(address.String(), port),
		)
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("OpenAPI host %q did not resolve to an address", host)
}

func (loader *openAPILoader) loadDocument(
	requestedURL *url.URL,
) (*loadedOpenAPIDocument, error) {
	requestedKey := documentURLString(requestedURL)
	if cached := loader.documents[requestedKey]; cached != nil {
		return cached, nil
	}
	if loader.documentCount >= maxOpenAPIDocuments {
		return nil, fmt.Errorf("OpenAPI import exceeds the %d document limit", maxOpenAPIDocuments)
	}

	finalURL, body, err := loader.fetchDocument(requestedURL)
	if err != nil {
		return nil, err
	}
	if loader.totalBytes+len(body) > maxOpenAPITotalBytes {
		return nil, fmt.Errorf(
			"OpenAPI import exceeds the %d-byte total document limit",
			maxOpenAPITotalBytes,
		)
	}
	loader.totalBytes += len(body)

	document, err := decodeOpenAPIDocument(body)
	if err != nil {
		return nil, err
	}
	loaded := &loadedOpenAPIDocument{
		url:  documentURLString(finalURL),
		root: document,
	}
	loader.documentCount++
	loader.documents[requestedKey] = loaded
	loader.documents[loaded.url] = loaded
	return loaded, nil
}

func (loader *openAPILoader) fetchDocument(
	initialURL *url.URL,
) (*url.URL, []byte, error) {
	current := cloneURL(initialURL)
	for {
		if err := validateOpenAPINetworkTarget(
			loader.ctx,
			current,
			loader.request.AllowPrivateNetwork,
		); err != nil {
			return nil, nil, err
		}

		req, err := http.NewRequestWithContext(
			loader.ctx,
			http.MethodGet,
			documentURLString(current),
			nil,
		)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/json, application/yaml, text/yaml;q=0.9, */*;q=0.1")

		resp, err := loader.client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("fetch OpenAPI document: %w", err)
		}
		if isOpenAPIRedirect(resp.StatusCode) {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if !loader.request.AllowRedirects {
				return nil, nil, errors.New(
					"OpenAPI document redirect requires explicit redirect consent",
				)
			}
			if loader.redirectCount >= maxOpenAPIRedirects {
				return nil, nil, fmt.Errorf(
					"OpenAPI document exceeds the %d-redirect limit",
					maxOpenAPIRedirects,
				)
			}
			loader.redirectCount++
			if strings.TrimSpace(location) == "" {
				return nil, nil, errors.New("OpenAPI document redirect is missing a Location header")
			}
			next, err := current.Parse(location)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid OpenAPI redirect URL: %w", err)
			}
			current, err = parseOpenAPIURL(next.String())
			if err != nil {
				return nil, nil, fmt.Errorf("invalid OpenAPI redirect destination: %w", err)
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			return nil, nil, fmt.Errorf("fetch OpenAPI document: HTTP %d", resp.StatusCode)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAPIBodyBytes+1))
		closeErr := resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read OpenAPI document: %w", err)
		}
		if closeErr != nil {
			return nil, nil, fmt.Errorf("close OpenAPI document response: %w", closeErr)
		}
		if len(body) > maxOpenAPIBodyBytes {
			return nil, nil, fmt.Errorf(
				"OpenAPI document is larger than %d bytes",
				maxOpenAPIBodyBytes,
			)
		}
		return current, body, nil
	}
}

func parseOpenAPIURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, errors.New("OpenAPI URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenAPI URL: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("OpenAPI URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("OpenAPI URL requires a host")
	}
	if parsed.User != nil {
		return nil, errors.New("OpenAPI URL must not include credentials")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("OpenAPI document URL must not include a fragment")
	}
	return parsed, nil
}

func validateOpenAPINetworkTarget(
	ctx context.Context,
	target *url.URL,
	allowPrivateNetwork bool,
) error {
	if allowPrivateNetwork {
		return nil
	}
	host := target.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return fmt.Errorf("OpenAPI host %q requires private-network consent", host)
	}
	addresses, err := resolveOpenAPIHost(ctx, host)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if isUnsafeOpenAPIAddress(address) {
			return fmt.Errorf("OpenAPI host %q requires private-network consent", host)
		}
	}
	return nil
}

func resolveOpenAPIHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	addresses, err := openAPILookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve OpenAPI host %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("OpenAPI host %q did not resolve to an address", host)
	}
	for index := range addresses {
		addresses[index] = addresses[index].Unmap()
	}
	return addresses, nil
}

func isUnsafeOpenAPIAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return true
	}
	for _, blocked := range blockedOpenAPINetworks {
		if blocked.Contains(address) {
			return true
		}
	}
	return false
}

func decodeOpenAPIDocument(body []byte) (map[string]any, error) {
	if document, err := decodeOpenAPIJSON(body); err == nil {
		return document, nil
	}

	decoder := yaml.NewDecoder(bytes.NewReader(body))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("OpenAPI document must be valid JSON or YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("OpenAPI input must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("decode trailing OpenAPI YAML document: %w", err)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("normalize OpenAPI YAML document: %w", err)
	}
	document, err := decodeOpenAPIJSON(normalized)
	if err != nil {
		return nil, fmt.Errorf("OpenAPI document root must be an object: %w", err)
	}
	return document, nil
}

func decodeOpenAPIJSON(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("OpenAPI input must contain exactly one document")
		}
		return nil, err
	}
	if document == nil {
		return nil, errors.New("OpenAPI document root must be an object")
	}
	return document, nil
}

func (loader *openAPILoader) resolveValue(
	value any,
	baseURL string,
	stack map[string]bool,
	referenceDepth int,
	structureDepth int,
) (any, error) {
	if referenceDepth > maxOpenAPIReferenceDepth {
		return nil, fmt.Errorf(
			"OpenAPI reference depth exceeds the %d-level limit",
			maxOpenAPIReferenceDepth,
		)
	}
	if structureDepth > maxOpenAPIStructureDepth {
		return nil, fmt.Errorf(
			"OpenAPI structure depth exceeds the %d-level limit",
			maxOpenAPIStructureDepth,
		)
	}
	loader.resolvedNodes++
	if loader.resolvedNodes > maxOpenAPIResolvedNodes {
		return nil, fmt.Errorf(
			"OpenAPI resolved structure exceeds the %d-node limit",
			maxOpenAPIResolvedNodes,
		)
	}

	switch typed := value.(type) {
	case map[string]any:
		if rawReference, exists := typed["$ref"]; exists {
			reference, ok := rawReference.(string)
			if !ok || strings.TrimSpace(reference) == "" {
				return nil, errors.New("OpenAPI $ref must be a non-empty string")
			}
			return loader.resolveReference(
				typed,
				reference,
				baseURL,
				stack,
				referenceDepth,
				structureDepth,
			)
		}
		resolved := make(map[string]any, len(typed))
		for _, key := range sortedOpenAPIObjectKeys(typed) {
			next, err := loader.resolveValue(
				typed[key],
				baseURL,
				stack,
				referenceDepth,
				structureDepth+1,
			)
			if err != nil {
				return nil, err
			}
			resolved[key] = next
		}
		return resolved, nil
	case []any:
		resolved := make([]any, len(typed))
		for index, item := range typed {
			next, err := loader.resolveValue(
				item,
				baseURL,
				stack,
				referenceDepth,
				structureDepth+1,
			)
			if err != nil {
				return nil, err
			}
			resolved[index] = next
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func (loader *openAPILoader) resolveReference(
	referenceObject map[string]any,
	reference string,
	baseURL string,
	stack map[string]bool,
	referenceDepth int,
	structureDepth int,
) (any, error) {
	loader.referenceCount++
	if loader.referenceCount > maxOpenAPIReferences {
		return nil, fmt.Errorf(
			"OpenAPI import exceeds the %d-reference limit",
			maxOpenAPIReferences,
		)
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenAPI reference base URL: %w", err)
	}
	parsedReference, err := url.Parse(reference)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenAPI reference %q: %w", safeReferenceLabel(reference), err)
	}
	target := base.ResolveReference(parsedReference)
	fragment := target.Fragment
	target.Fragment = ""
	target.RawFragment = ""
	target, err = parseOpenAPIURL(target.String())
	if err != nil {
		return nil, fmt.Errorf(
			"invalid OpenAPI reference %q: %w",
			safeReferenceLabel(reference),
			err,
		)
	}
	targetKey := documentURLString(target)
	if targetKey != documentURLString(base) && !loader.request.AllowExternalRefs {
		return nil, fmt.Errorf(
			"external OpenAPI reference %q requires explicit external-reference consent",
			safeReferenceLabel(reference),
		)
	}

	document, err := loader.loadDocument(target)
	if err != nil {
		return nil, fmt.Errorf(
			"load OpenAPI reference %q: %w",
			safeReferenceLabel(reference),
			err,
		)
	}
	referenceKey := document.url + "#" + fragment
	if stack[referenceKey] {
		return nil, fmt.Errorf(
			"cyclic OpenAPI reference detected at %q",
			safeReferenceLabel(reference),
		)
	}

	resolved, cached := loader.resolvedReferences[referenceKey]
	if !cached {
		pointed, err := resolveOpenAPIFragment(document.root, fragment)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve OpenAPI reference %q: %w",
				safeReferenceLabel(reference),
				err,
			)
		}
		stack[referenceKey] = true
		resolved, err = loader.resolveValue(
			pointed,
			document.url,
			stack,
			referenceDepth+1,
			structureDepth+1,
		)
		delete(stack, referenceKey)
		if err != nil {
			return nil, err
		}
		loader.resolvedReferences[referenceKey] = resolved
	}

	if len(referenceObject) == 1 {
		return resolved, nil
	}
	resolvedObject, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"OpenAPI reference %q has sibling fields but does not resolve to an object",
			safeReferenceLabel(reference),
		)
	}
	merged := make(map[string]any, len(resolvedObject)+len(referenceObject)-1)
	for key, value := range resolvedObject {
		merged[key] = value
	}
	for _, key := range sortedOpenAPIObjectKeys(referenceObject) {
		if key == "$ref" {
			continue
		}
		next, err := loader.resolveValue(
			referenceObject[key],
			baseURL,
			stack,
			referenceDepth,
			structureDepth+1,
		)
		if err != nil {
			return nil, err
		}
		merged[key] = next
	}
	return merged, nil
}

func resolveOpenAPIFragment(document map[string]any, fragment string) (any, error) {
	if fragment == "" {
		return document, nil
	}
	if strings.HasPrefix(fragment, "/") {
		return resolveOpenAPIJSONPointer(document, fragment)
	}
	return resolveOpenAPIAnchor(document, fragment)
}

func resolveOpenAPIJSONPointer(document map[string]any, pointer string) (any, error) {
	var current any = document
	for _, rawToken := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token := decodeJSONPointerToken(rawToken)
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, fmt.Errorf("JSON Pointer token %q was not found", token)
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("JSON Pointer array index %q is invalid", token)
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("JSON Pointer token %q cannot traverse %T", token, current)
		}
	}
	return current, nil
}

func resolveOpenAPIAnchor(document map[string]any, anchor string) (any, error) {
	var (
		found any
		count int
		nodes int
	)
	var visit func(any, int) error
	visit = func(value any, depth int) error {
		if depth > maxOpenAPIStructureDepth {
			return fmt.Errorf(
				"OpenAPI anchor structure depth exceeds the %d-level limit",
				maxOpenAPIStructureDepth,
			)
		}
		nodes++
		if nodes > maxOpenAPIAnchorScanNodes {
			return fmt.Errorf(
				"OpenAPI anchor scan exceeds the %d-node limit",
				maxOpenAPIAnchorScanNodes,
			)
		}
		switch typed := value.(type) {
		case map[string]any:
			if candidate, _ := typed["$anchor"].(string); candidate == anchor {
				found = typed
				count++
			}
			for _, key := range sortedOpenAPIObjectKeys(typed) {
				if err := visit(typed[key], depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range typed {
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(document, 0); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("OpenAPI anchor %q was not found", anchor)
	}
	if count > 1 {
		return nil, fmt.Errorf("OpenAPI anchor %q is duplicated", anchor)
	}
	return found, nil
}

func isOpenAPIRedirect(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func documentURLString(value *url.URL) string {
	documentURL := cloneURL(value)
	documentURL.Fragment = ""
	documentURL.RawFragment = ""
	return documentURL.String()
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}

func safeReferenceLabel(reference string) string {
	parsed, err := url.Parse(reference)
	if err != nil {
		return "<invalid>"
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	return parsed.String()
}

func sortedOpenAPIObjectKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
