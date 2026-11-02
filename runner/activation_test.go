package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestActivationFailuresStopVM(t *testing.T) {
	tests := []struct {
		failure string
		want    string
	}{
		{"activate", "activate guest system"},
		{"activation-system", "guest activated"},
		{"activation-nix", "Nix is unavailable after activation"},
	}
	for _, test := range tests {
		t.Run(test.failure, func(t *testing.T) {
			config := tartFixture(t)
			t.Setenv("NIX_DARWIN_VM_TEST_FAIL", test.failure)
			var output bytes.Buffer
			err := config.run(t.Context(), &output, &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q; output: %s", err, test.want, &output)
			}
			if strings.Contains(output.String(), "Ready:") {
				t.Fatal("failed activation reported readiness")
			}
			if test.failure == "activate" && !strings.Contains(output.String(), "fixture activation failed") {
				t.Fatal("activation diagnostic was not forwarded")
			}
			vm, err := readFakeVM(config.StateDirectory)
			if err != nil || vm.State != "stopped" || !vm.Interrupted {
				t.Fatalf("failed activation left the VM running: %+v, %v", vm, err)
			}
		})
	}
}
