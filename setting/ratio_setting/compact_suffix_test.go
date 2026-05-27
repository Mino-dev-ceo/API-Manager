package ratio_setting

import (
	"reflect"
	"testing"
)

func TestEquivalentMatchingModelNamesIncludesCompactFallback(t *testing.T) {
	got := EquivalentMatchingModelNames("gpt-5.4" + CompactModelSuffix)
	want := []string{"gpt-5.4" + CompactModelSuffix, "gpt-5.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EquivalentMatchingModelNames(compact) = %#v, want %#v", got, want)
	}
}
