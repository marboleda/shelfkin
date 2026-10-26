package main

import "testing"

func TestServiceName(t *testing.T) {
	if serviceName != "shelfkin-api" {
		t.Errorf("serviceName = %q, want %q", serviceName, "shelfkin-api")
	}
}
