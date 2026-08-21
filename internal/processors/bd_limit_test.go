package processors

import "testing"

func TestProcessorInvokesBD(t *testing.T) {
	tests := []struct {
		name string
		proc Processor
		want bool
	}{
		{name: "direct", proc: Processor{Command: "bd", Args: []string{"show", "x"}}, want: true},
		{name: "absolute", proc: Processor{Command: "/usr/local/bin/bd"}, want: true},
		{name: "shell", proc: Processor{Command: "sh", Args: []string{"-c", "out=$(bd prime); printf '%s' \"$out\""}}, want: true},
		{name: "unrelated", proc: Processor{Command: "sh", Args: []string{"-c", "printf done"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := processorInvokesBD(&tt.proc); got != tt.want {
				t.Fatalf("processorInvokesBD() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessorBDExecutable(t *testing.T) {
	tests := []struct {
		name string
		proc Processor
		want string
	}{
		{name: "direct", proc: Processor{Command: "bd"}, want: "bd"},
		{name: "absolute", proc: Processor{Command: "/opt/tools/bd"}, want: "/opt/tools/bd"},
		{name: "shell", proc: Processor{Command: "sh", Args: []string{"-c", "bd prime"}}, want: "bd"},
		{name: "unrelated", proc: Processor{Command: "sh", Args: []string{"-c", "printf done"}}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := processorBDExecutable(&tt.proc); got != tt.want {
				t.Fatalf("processorBDExecutable() = %q, want %q", got, tt.want)
			}
		})
	}
}
