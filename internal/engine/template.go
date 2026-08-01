package engine

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

var (
	templateOpenDelimiter  = []byte("{{")
	templateCloseDelimiter = []byte("}}")
)

type compiledTemplate struct {
	segments []compiledTemplateSegment
	sizeHint int
}

type compiledTemplateSegment struct {
	literal  []byte
	variable string
}

func compileTemplateBytes(value []byte) (compiledTemplate, []string, error) {
	var segments []compiledTemplateSegment
	var names []string
	var seen map[string]struct{}

	for offset := 0; offset < len(value); {
		remaining := value[offset:]
		startOffset := bytes.Index(remaining, templateOpenDelimiter)
		unexpectedClose := bytes.Index(remaining, templateCloseDelimiter)
		if startOffset < 0 {
			if unexpectedClose >= 0 {
				return compiledTemplate{}, nil, errors.New("contains an unexpected closing delimiter")
			}
			if segments != nil && offset < len(value) {
				segments = append(segments, compiledTemplateSegment{literal: value[offset:]})
			}
			break
		}
		if unexpectedClose >= 0 && unexpectedClose < startOffset {
			return compiledTemplate{}, nil, errors.New("contains an unexpected closing delimiter")
		}

		start := offset + startOffset
		endOffset := bytes.Index(value[start+len(templateOpenDelimiter):], templateCloseDelimiter)
		if endOffset < 0 {
			return compiledTemplate{}, nil, errors.New("contains an unclosed template")
		}
		end := start + len(templateOpenDelimiter) + endOffset
		name := strings.TrimSpace(string(value[start+len(templateOpenDelimiter) : end]))
		if err := validateVariableSyntax(name); err != nil {
			return compiledTemplate{}, nil, fmt.Errorf("variable %q: %w", name, err)
		}

		segments = append(segments, compiledTemplateSegment{
			literal:  value[offset:start],
			variable: name,
		})
		if seen == nil {
			seen = make(map[string]struct{})
		}
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			names = append(names, name)
		}
		offset = end + len(templateCloseDelimiter)
	}

	return compiledTemplate{
		segments: segments,
		sizeHint: len(value),
	}, names, nil
}

func (template compiledTemplate) dynamic() bool {
	return len(template.segments) > 0
}

func (variables *workerVariables) render(template compiledTemplate) ([]byte, error) {
	rendered := variables.renderBuffer
	if cap(rendered) < template.sizeHint {
		rendered = make([]byte, 0, template.sizeHint)
	} else {
		rendered = rendered[:0]
	}
	for _, segment := range template.segments {
		rendered = append(rendered, segment.literal...)
		if segment.variable == "" {
			continue
		}
		value, ok := variables.value(segment.variable)
		if !ok {
			variables.renderBuffer = rendered
			return nil, fmt.Errorf(
				"template variable %q is unavailable for this iteration",
				segment.variable,
			)
		}
		rendered = append(rendered, value...)
	}
	variables.renderBuffer = rendered
	return rendered, nil
}

const maxRetainedRenderBufferBytes = 64 << 10

func (variables *workerVariables) releaseRenderBuffer() {
	if cap(variables.renderBuffer) > maxRetainedRenderBufferBytes {
		variables.renderBuffer = nil
	}
}
