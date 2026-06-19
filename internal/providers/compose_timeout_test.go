package providers

import "testing"

func TestComposeTimeoutForArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "config remains short",
			args: []string{"-f", "compose.yaml", "--profile", "dev", "config"},
			want: "short",
		},
		{
			name: "pull is long",
			args: []string{"-f", "compose.yaml", "pull"},
			want: "long",
		},
		{
			name: "up after equals flag is long",
			args: []string{"--profile=dev", "up", "-d"},
			want: "long",
		},
		{
			name: "ps remains short",
			args: []string{"--project-name", "demo", "ps", "--format", "json"},
			want: "short",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := composeTimeoutForArgs(tt.args)
			if tt.want == "long" && got != dockerOperationTimeout {
				t.Fatalf("timeout = %s, want %s", got, dockerOperationTimeout)
			}
			if tt.want == "short" && got != composeCommandTimeout {
				t.Fatalf("timeout = %s, want %s", got, composeCommandTimeout)
			}
		})
	}
}
