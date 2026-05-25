package domainutil

import (
	"strings"
	"testing"
)

func TestValidateNormalizes(t *testing.T) {
	got, err := Validate("  ExAmPle.Test  ")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got != "example.test" {
		t.Fatalf("Validate() = %q, want %q", got, "example.test")
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: "domain is required"},
		{name: "blank", value: "   ", want: "domain is required"},
		{name: "contains at", value: "bad@domain", want: "domain must not contain @"},
		{name: "internal whitespace", value: "bad domain", want: "domain must not contain whitespace"},
		{name: "leading dot", value: ".example.test", want: "domain must not start or end with '.' or contain consecutive dots"},
		{name: "trailing dot", value: "example.test.", want: "domain must not start or end with '.' or contain consecutive dots"},
		{name: "consecutive dots", value: "example..test", want: "domain must not start or end with '.' or contain consecutive dots"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.value)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}
