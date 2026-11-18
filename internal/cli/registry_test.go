package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildTargetKnownKinds(t *testing.T) {
	cases := map[string]bool{"": false, "external": false, "image": true, "compose": true}
	for kind, wantProvisioner := range cases {
		provisioner, err := buildTarget(TargetFileConfig{Kind: kind})
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		if (provisioner != nil) != wantProvisioner {
			t.Fatalf("kind %q provisioner=%v, want set=%v", kind, provisioner != nil, wantProvisioner)
		}
	}
}

func TestBuildTargetUnknownKindFailsClosed(t *testing.T) {
	_, err := buildTarget(TargetFileConfig{Kind: "kubernetes"})
	if err == nil {
		t.Fatal("unknown target kind accepted")
	}
	for _, want := range []string{"unsupported target kind", "compose", "external", "image"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestBuildRuntimeDefaultsToPi(t *testing.T) {
	runtime, err := buildRuntime(nil)
	if err != nil {
		t.Fatalf("default runtime: %v", err)
	}
	if runtime == nil {
		t.Fatal("default runtime is nil")
	}
	explicit, err := buildRuntime(json.RawMessage(`{"type":"pi","provider":"x"}`))
	if err != nil {
		t.Fatalf("explicit pi runtime: %v", err)
	}
	if explicit == nil {
		t.Fatal("explicit pi runtime is nil")
	}
}

func TestBuildRuntimeUnknownTypeFailsClosed(t *testing.T) {
	_, err := buildRuntime(json.RawMessage(`{"type":"crewai"}`))
	if err == nil {
		t.Fatal("unknown runtime type accepted")
	}
	if !strings.Contains(err.Error(), "unsupported agent runtime") {
		t.Fatalf("err = %v, want unsupported-runtime rejection", err)
	}
}

func TestBuildRuntimeRejectsInvalidPiConfig(t *testing.T) {
	if _, err := buildRuntime(json.RawMessage(`{"type":"pi","provider":123}`)); err == nil {
		t.Fatal("invalid pi config accepted")
	}
}
