package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPreflightUsesRuntimeBindingsWithoutReturningValues(t *testing.T) {
	request := validPreflightRequest()
	request.Config.URL += "/secure?token={{SECRET_TOKEN}}"
	request.RuntimeBindings = map[string]string{"SECRET_TOKEN": "do-not-return-this-value"}

	response, err := preflightStartRequest(request)
	if err != nil {
		t.Fatalf("preflight with runtime binding: %v", err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), request.RuntimeBindings["SECRET_TOKEN"]) {
		t.Fatal("preflight response exposed a runtime binding value")
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requestJSON), request.RuntimeBindings["SECRET_TOKEN"]) || strings.Contains(string(requestJSON), "runtimeBindings") {
		t.Fatal("start request serialized runtime bindings")
	}

	request.RuntimeBindings = nil
	if _, err := preflightStartRequest(request); err == nil {
		t.Fatal("expected an undefined runtime binding error")
	}
}
