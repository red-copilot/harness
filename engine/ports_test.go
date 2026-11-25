package engine

import (
	"reflect"
	"sort"
	"testing"
)

func TestStoreSurfaceIsNarrow(t *testing.T) {
	typ := reflect.TypeOf((*Store)(nil)).Elem()
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	sort.Strings(got)
	want := []string{
		"AppendEvent", "AppendLog", "Close", "Commit", "CreateRun",
		"Finalize", "GetRun", "ListArtifacts", "MarkInterrupted",
		"OpenArtifact", "RestoreBoardState", "UpdateRun",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Store methods = %v, want %v", got, want)
	}
}
