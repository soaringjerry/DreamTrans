package modelcatalog

import (
	"reflect"
	"testing"
)

func TestModelsEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    string
		want    string
		wantErr bool
	}{
		{name: "version root", base: "https://api.openai.com/v1", want: "https://api.openai.com/v1/models"},
		{name: "trailing slash", base: "https://gateway.example/v1/", want: "https://gateway.example/v1/models"},
		{name: "query removed", base: "https://gateway.example/v1?tenant=one", want: "https://gateway.example/v1/models"},
		{name: "relative rejected", base: "/v1", wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := modelsEndpoint(test.base)
			if test.wantErr {
				if err == nil {
					t.Fatalf("modelsEndpoint(%q) unexpectedly succeeded: %q", test.base, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("modelsEndpoint(%q): %v", test.base, err)
			}
			if got != test.want {
				t.Fatalf("modelsEndpoint(%q) = %q, want %q", test.base, got, test.want)
			}
		})
	}
}

func TestSortAvailable(t *testing.T) {
	t.Parallel()

	models := []AvailableModel{
		{ModelID: "z-chat", Purpose: "chat"},
		{ModelID: "a-chat", Purpose: "chat", IsDefault: true},
		{ModelID: "b-summary", Purpose: "summary"},
		{ModelID: "a-summary", Purpose: "summary"},
	}
	SortAvailable(models)
	want := []AvailableModel{
		{ModelID: "a-chat", Purpose: "chat", IsDefault: true},
		{ModelID: "z-chat", Purpose: "chat"},
		{ModelID: "a-summary", Purpose: "summary"},
		{ModelID: "b-summary", Purpose: "summary"},
	}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("SortAvailable() = %#v, want %#v", models, want)
	}
}
