package common

import (
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestGetEndpointTypesByChannelTypeCodex(t *testing.T) {
	got := GetEndpointTypesByChannelType(constant.ChannelTypeCodex, "gpt-5.4")
	want := []constant.EndpointType{
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetEndpointTypesByChannelType(Codex) = %#v, want %#v", got, want)
	}
}
