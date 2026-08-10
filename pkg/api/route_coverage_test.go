package client

import (
	"reflect"
	"testing"
)

// TestRouteCoverage_ConversationCentricSurface asserts every route claimed
// in RouteCoverage (coverage.go) has a matching exported method on *Client,
// per the Test phase's coverage requirement.
func TestRouteCoverage_ConversationCentricSurface(t *testing.T) {
	clientType := reflect.TypeOf(&Client{})
	for route, methodName := range RouteCoverage {
		t.Run(route, func(t *testing.T) {
			if _, ok := clientType.MethodByName(methodName); !ok {
				t.Errorf("route %q claims SDK method %q, but *Client has no such method", route, methodName)
			}
		})
	}
}

// TestRouteCoverage_NoDuplicateMethodNames guards against copy-paste errors
// in RouteCoverage (two routes accidentally pointing at the same method name
// would silently under-count real coverage).
func TestRouteCoverage_NoDuplicateMethodNames(t *testing.T) {
	seen := make(map[string]string, len(RouteCoverage))
	for route, methodName := range RouteCoverage {
		if other, ok := seen[methodName]; ok {
			t.Errorf("method %q is claimed by both %q and %q", methodName, other, route)
		}
		seen[methodName] = route
	}
}
